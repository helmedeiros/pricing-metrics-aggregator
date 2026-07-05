// Package prom wires the pricing-metrics-aggregator Prometheus surface.
// v0.0.1 shipped env-only gauges; v0.0.2 adds a parallel labeled set
// registered dynamically based on the operator's --include-labels flag
// per ADR-0003. The env-only gauges continue to publish the totals
// unchanged so existing Grafana panels keep working.
package prom

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/helmedeiros/pricing-metrics-aggregator/internal/rollup"
)

// Metrics is the aggregator's observability surface. Every rollup pass
// overwrites the last-window gauges via Publish(); the runs_total
// counter is monotonic.
type Metrics struct {
	// Env-only totals — always present.
	ImpressionsLastWindow       *prometheus.GaugeVec
	PurchasesLastWindow         *prometheus.GaugeVec
	WalkoffsAtInitLastWindow    *prometheus.GaugeVec
	WalkoffsAtReserveLastWindow *prometheus.GaugeVec
	GMVLastWindowEUR            *prometheus.GaugeVec
	ConversionRateLastWindow    *prometheus.GaugeVec

	// Labeled per-tuple gauges — present when the operator opted labels in.
	// Registered with the (env + opted-in labels) variable label set.
	// Nil when sel.Any() is false.
	labeledImpressions   *prometheus.GaugeVec
	labeledPurchases     *prometheus.GaugeVec
	labeledWalkoffsInit  *prometheus.GaugeVec
	labeledWalkoffsRes   *prometheus.GaugeVec
	labeledGMV           *prometheus.GaugeVec
	labeledConversion    *prometheus.GaugeVec

	RunsTotal          *prometheus.CounterVec
	RunDurationSeconds prometheus.Histogram

	sel     rollup.LabelSelection
	handler http.Handler
}

// New wires the registry. Passing an empty LabelSelection yields the
// v0.0.1 env-only surface bit-for-bit. Passing a populated selection
// adds parallel per-tuple gauges under names ending in `_labeled`.
func New(env string, sel rollup.LabelSelection) *Metrics {
	reg := prometheus.NewRegistry()
	constLabels := prometheus.Labels{}
	if env != "" {
		constLabels["env"] = env
	}
	m := &Metrics{
		ImpressionsLastWindow: newTotalGauge(constLabels,
			"pricing_impressions_last_window",
			"Priced offers shown in the most recent aggregation window (search.v1 offers array length)"),
		PurchasesLastWindow: newTotalGauge(constLabels,
			"pricing_purchases_last_window",
			"booking.purchased events in the most recent aggregation window"),
		WalkoffsAtInitLastWindow: newTotalGauge(constLabels,
			"pricing_walkoffs_at_init_last_window",
			"booking.timeout events with phase=initiated in the most recent window (price-shock cohort)"),
		WalkoffsAtReserveLastWindow: newTotalGauge(constLabels,
			"pricing_walkoffs_at_reserve_last_window",
			"booking.timeout events with phase=reserved in the most recent window (payment-friction cohort)"),
		GMVLastWindowEUR: newTotalGauge(constLabels,
			"pricing_gmv_last_window_eur",
			"Sum of revenue on booking.purchased events in the most recent window (EUR-assumed)"),
		ConversionRateLastWindow: newTotalGauge(constLabels,
			"pricing_conversion_rate_last_window",
			"purchases / impressions in the most recent window; 0 when there are no impressions"),
		RunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "pricing_metrics_aggregator_runs_total",
			Help:        "Aggregation runs completed, keyed by outcome (ok | error)",
			ConstLabels: constLabels,
		}, []string{"outcome"}),
		RunDurationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "pricing_metrics_aggregator_run_duration_seconds",
			Help:        "Wall-clock cost of one aggregation run (ingest + rollup)",
			ConstLabels: constLabels,
			Buckets:     []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}),
		sel: sel,
	}
	reg.MustRegister(
		m.ImpressionsLastWindow, m.PurchasesLastWindow,
		m.WalkoffsAtInitLastWindow, m.WalkoffsAtReserveLastWindow,
		m.GMVLastWindowEUR, m.ConversionRateLastWindow,
		m.RunsTotal, m.RunDurationSeconds,
	)

	if sel.Any() {
		names := labelNamesAsStrings(sel)
		m.labeledImpressions = newLabeledGauge(constLabels, names,
			"pricing_impressions_last_window_labeled",
			"Per-tuple priced offers shown in the most recent window (see ADR-0003 for cardinality caps)")
		m.labeledPurchases = newLabeledGauge(constLabels, names,
			"pricing_purchases_last_window_labeled",
			"Per-tuple booking.purchased events in the most recent window")
		m.labeledWalkoffsInit = newLabeledGauge(constLabels, names,
			"pricing_walkoffs_at_init_last_window_labeled",
			"Per-tuple booking.timeout{phase=initiated} in the most recent window")
		m.labeledWalkoffsRes = newLabeledGauge(constLabels, names,
			"pricing_walkoffs_at_reserve_last_window_labeled",
			"Per-tuple booking.timeout{phase=reserved} in the most recent window")
		m.labeledGMV = newLabeledGauge(constLabels, names,
			"pricing_gmv_last_window_eur_labeled",
			"Per-tuple sum of revenue on booking.purchased events in the most recent window")
		m.labeledConversion = newLabeledGauge(constLabels, names,
			"pricing_conversion_rate_last_window_labeled",
			"Per-tuple purchases / impressions in the most recent window")
		reg.MustRegister(
			m.labeledImpressions, m.labeledPurchases,
			m.labeledWalkoffsInit, m.labeledWalkoffsRes,
			m.labeledGMV, m.labeledConversion,
		)
	}

	m.handler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return m
}

func newTotalGauge(constLabels prometheus.Labels, name, help string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name, Help: help, ConstLabels: constLabels,
	}, []string{})
}

func newLabeledGauge(constLabels prometheus.Labels, labelNames []string, name, help string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name, Help: help, ConstLabels: constLabels,
	}, labelNames)
}

func labelNamesAsStrings(sel rollup.LabelSelection) []string {
	names := sel.Names()
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return out
}

// PublishTotal sets the env-only gauges to the window's Total bucket.
// Call every tick regardless of label selection.
func (m *Metrics) PublishTotal(b rollup.Bucket) {
	m.ImpressionsLastWindow.WithLabelValues().Set(float64(b.Impressions))
	m.PurchasesLastWindow.WithLabelValues().Set(float64(b.Purchases))
	m.WalkoffsAtInitLastWindow.WithLabelValues().Set(float64(b.WalkoffsAtInit))
	m.WalkoffsAtReserveLastWindow.WithLabelValues().Set(float64(b.WalkoffsAtReserve))
	m.GMVLastWindowEUR.WithLabelValues().Set(b.GMV)
	m.ConversionRateLastWindow.WithLabelValues().Set(b.ConversionRate())
}

// PublishLabeled resets the labeled gauge vectors and sets one series
// per LabeledBucket in `emit`. Reset every tick so tuples dropping out
// of the top-N do not linger as stale series.
//
// Caller passes the already-topN'd, already-ordered slice from
// rollup.TopN(). No-op when the operator did not opt into labels.
func (m *Metrics) PublishLabeled(emit []rollup.LabeledBucket) {
	if !m.sel.Any() || m.labeledImpressions == nil {
		return
	}
	m.labeledImpressions.Reset()
	m.labeledPurchases.Reset()
	m.labeledWalkoffsInit.Reset()
	m.labeledWalkoffsRes.Reset()
	m.labeledGMV.Reset()
	m.labeledConversion.Reset()
	for _, e := range emit {
		values := e.Key.Values(m.sel)
		m.labeledImpressions.WithLabelValues(values...).Set(float64(e.Bucket.Impressions))
		m.labeledPurchases.WithLabelValues(values...).Set(float64(e.Bucket.Purchases))
		m.labeledWalkoffsInit.WithLabelValues(values...).Set(float64(e.Bucket.WalkoffsAtInit))
		m.labeledWalkoffsRes.WithLabelValues(values...).Set(float64(e.Bucket.WalkoffsAtReserve))
		m.labeledGMV.WithLabelValues(values...).Set(e.Bucket.GMV)
		m.labeledConversion.WithLabelValues(values...).Set(e.Bucket.ConversionRate())
	}
}

func (m *Metrics) Handler() http.Handler { return m.handler }
