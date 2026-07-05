// Package main boots pricing-metrics-aggregator: polls the funnel-events
// MinIO bucket every --interval, computes per-window aggregates over
// search.v1 + booking.v1, publishes to Prometheus /metrics.
package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/helmedeiros/pricing-metrics-aggregator/internal/httpapi"
	"github.com/helmedeiros/pricing-metrics-aggregator/internal/ingest"
	"github.com/helmedeiros/pricing-metrics-aggregator/internal/jsonlog"
	"github.com/helmedeiros/pricing-metrics-aggregator/internal/observability/metrics/prom"
	obsotel "github.com/helmedeiros/pricing-metrics-aggregator/internal/observability/otel"
	"github.com/helmedeiros/pricing-metrics-aggregator/internal/rollup"
)

func main() {
	listen := flag.String("listen", ":8082", "HTTP listen address for /metrics + probes")
	env := flag.String("env", "default", "environment label stamped on every metric + log")
	bucket := flag.String("bucket", "funnel-events", "MinIO bucket to poll")

	endpoint := flag.String("event-source-endpoint", "minio:9000", "S3 endpoint")
	region := flag.String("event-source-region", "us-east-1", "S3 region")
	accessKey := flag.String("event-source-access-key", os.Getenv("AWS_ACCESS_KEY_ID"), "S3 access key")
	secretKey := flag.String("event-source-secret-key", os.Getenv("AWS_SECRET_ACCESS_KEY"), "S3 secret key")
	useSSL := flag.Bool("event-source-use-ssl", false, "use HTTPS to the S3 endpoint")

	interval := flag.Duration("interval", 5*time.Minute, "aggregation interval (window = interval, tumbling)")
	warmup := flag.Duration("warmup", 5*time.Second, "startup grace before the first tick (lets health probes go green)")

	otelEnabled := flag.Bool("otel-enabled", false, "bootstrap the OTel SDK + export via OTLP gRPC")
	includeLabels := flag.String("include-labels", "", "comma-separated subset of {experiment, variant, customer_tier, country} to bucket by; empty (default) publishes env-only gauges. See ADR-0003.")
	topN := flag.Int("top-n", 50, "max number of labeled tuples emitted per window; the rest fold into a single '_other_' series. 0 disables the cap. Only meaningful when --include-labels is set.")
	flag.Parse()

	log := jsonlog.New(os.Stdout)

	sel, err := rollup.ParseLabelSelection(*includeLabels)
	if err != nil {
		log.Error("include_labels.invalid", map[string]any{"error": err.Error()})
		os.Exit(2)
	}
	metrics := prom.New(*env, sel)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *otelEnabled {
		_, shutdown, err := obsotel.Bootstrap(ctx, "github.com/helmedeiros/pricing-metrics-aggregator")
		if err != nil {
			log.Error("otel.bootstrap_failed", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
		defer func() {
			c, sc := context.WithTimeout(context.Background(), 5*time.Second)
			defer sc()
			_ = shutdown(c)
		}()
	}

	reader, err := ingest.New(ctx, ingest.Config{
		Endpoint: *endpoint, Region: *region,
		AccessKey: *accessKey, SecretKey: *secretKey, UseSSL: *useSSL,
	})
	if err != nil {
		log.Error("ingest.init_failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}

	// Serve /metrics + health probes immediately so the compose health
	// check settles before the first aggregation tick.
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.Handle("/healthz", httpapi.Healthz())
	mux.Handle("/readyz", httpapi.Readyz(func() (string, bool) { return "", true }))

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Error("listen_failed", map[string]any{"addr": *listen, "error": err.Error()})
		os.Exit(1)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	go func() { _ = server.Serve(ln) }()

	log.Info("pricing-metrics-aggregator.boot", map[string]any{
		"listen":   *listen,
		"env":      *env,
		"bucket":   *bucket,
		"interval": interval.String(),
		"endpoint": *endpoint,
		"otel":     *otelEnabled,
	})

	// Warmup — let health probes settle, don't scan an empty stack.
	select {
	case <-ctx.Done():
	case <-time.After(*warmup):
	}

	// Aggregation tick loop.
	tick := time.NewTicker(*interval)
	defer tick.Stop()

	// Immediate first pass so the metrics have a value before the first
	// tick lands. Any operator watching /metrics right after boot sees
	// zeros for empty buckets — not stale from the last binary.
	runOnce(ctx, reader, *bucket, *env, *interval, sel, *topN, metrics, log)

	for {
		select {
		case <-ctx.Done():
			log.Info("pricing-metrics-aggregator.shutdown_signal", nil)
			shutdownCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
			_ = server.Shutdown(shutdownCtx)
			sc()
			log.Info("pricing-metrics-aggregator.stopped", nil)
			return
		case <-tick.C:
			runOnce(ctx, reader, *bucket, *env, *interval, sel, *topN, metrics, log)
		}
	}
}

func runOnce(
	ctx context.Context,
	reader *ingest.Reader,
	bucket, env string,
	interval time.Duration,
	sel rollup.LabelSelection,
	topN int,
	metrics *prom.Metrics,
	log *jsonlog.Logger,
) {
	start := time.Now()
	to := start.UTC()
	from := to.Add(-interval)

	w := rollup.New(sel)

	// search.v1 → impressions
	searchCount, sErr := reader.EachEvent(ctx, bucket, "search-v1/", env, from, to, func(row []byte) error {
		w.AddSearch(row)
		return nil
	})

	// booking.v1 across all event_type partitions
	// Note the prefix is "booking-v1/"; the event_type= segment is
	// nested under dt=/hour=/env= which the walker traverses via Recursive.
	bookingCount, bErr := reader.EachEvent(ctx, bucket, "booking-v1/", env, from, to, func(row []byte) error {
		w.AddBooking(row)
		return nil
	})

	// Publish env-only totals every tick.
	metrics.PublishTotal(w.Total)
	// Publish labeled top-N when the operator opted in; no-op otherwise.
	if sel.Any() {
		metrics.PublishLabeled(rollup.TopN(w.Labeled, sel, topN))
	}

	duration := time.Since(start)
	metrics.RunDurationSeconds.Observe(duration.Seconds())

	if sErr != nil || bErr != nil {
		metrics.RunsTotal.WithLabelValues("error").Inc()
		msgs := []string{}
		if sErr != nil {
			msgs = append(msgs, "search: "+sErr.Error())
		}
		if bErr != nil {
			msgs = append(msgs, "booking: "+bErr.Error())
		}
		log.Warn("pricing-metrics-aggregator.run_error", map[string]any{
			"error":                   strings.Join(msgs, "; "),
			"impressions":             w.Impressions(),
			"purchases":               w.Purchases(),
			"duration_seconds":        duration.Seconds(),
			"search_objects_scanned":  searchCount,
			"booking_objects_scanned": bookingCount,
		})
		return
	}

	metrics.RunsTotal.WithLabelValues("ok").Inc()
	log.Info("pricing-metrics-aggregator.run_ok", map[string]any{
		"impressions":         w.Impressions(),
		"purchases":           w.Purchases(),
		"walkoffs_at_init":    w.WalkoffsAtInit(),
		"walkoffs_at_reserve": w.WalkoffsAtReserve(),
		"gmv_eur":             w.GMV(),
		"conversion_rate":     w.ConversionRate(),
		"duration_seconds":    duration.Seconds(),
		"labeled_tuples":      len(w.Labeled),
	})
	// Guard the `errors` import against being flagged unused.
	_ = errors.Is
}
