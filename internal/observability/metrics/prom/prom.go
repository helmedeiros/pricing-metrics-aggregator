// Package prom wires the pricing-metrics-aggregator Prometheus surface:
// last-window gauges for the aggregate metrics + one counter tracking
// how many aggregation runs have completed successfully.
package prom

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the aggregator's observability surface. Every rollup pass
// overwrites the last-window gauges; the runs_total counter monotonic.
type Metrics struct {
	ImpressionsLastWindow       *prometheus.GaugeVec
	PurchasesLastWindow         *prometheus.GaugeVec
	WalkoffsAtInitLastWindow    *prometheus.GaugeVec
	WalkoffsAtReserveLastWindow *prometheus.GaugeVec
	GMVLastWindowEUR            *prometheus.GaugeVec
	ConversionRateLastWindow    *prometheus.GaugeVec

	RunsTotal   *prometheus.CounterVec
	RunDurationSeconds prometheus.Histogram

	handler http.Handler
}

// New wires the registry and returns the metrics handles. env is
// stamped as a constant label so a multi-env deployment can scrape one
// aggregator per env and Grafana keys off the label.
func New(env string) *Metrics {
	reg := prometheus.NewRegistry()
	labels := prometheus.Labels{}
	if env != "" {
		labels["env"] = env
	}
	m := &Metrics{
		ImpressionsLastWindow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "pricing_impressions_last_window",
			Help:        "Priced offers shown in the most recent aggregation window (search.v1 offers array length)",
			ConstLabels: labels,
		}, []string{}),
		PurchasesLastWindow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "pricing_purchases_last_window",
			Help:        "booking.purchased events in the most recent aggregation window",
			ConstLabels: labels,
		}, []string{}),
		WalkoffsAtInitLastWindow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "pricing_walkoffs_at_init_last_window",
			Help:        "booking.timeout events with phase=initiated in the most recent window (price-shock cohort)",
			ConstLabels: labels,
		}, []string{}),
		WalkoffsAtReserveLastWindow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "pricing_walkoffs_at_reserve_last_window",
			Help:        "booking.timeout events with phase=reserved in the most recent window (payment-friction cohort)",
			ConstLabels: labels,
		}, []string{}),
		GMVLastWindowEUR: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "pricing_gmv_last_window_eur",
			Help:        "Sum of revenue on booking.purchased events in the most recent window (EUR-assumed)",
			ConstLabels: labels,
		}, []string{}),
		ConversionRateLastWindow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "pricing_conversion_rate_last_window",
			Help:        "purchases / impressions in the most recent window; 0 when there are no impressions",
			ConstLabels: labels,
		}, []string{}),
		RunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "pricing_metrics_aggregator_runs_total",
			Help:        "Aggregation runs completed, keyed by outcome (ok | error)",
			ConstLabels: labels,
		}, []string{"outcome"}),
		RunDurationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "pricing_metrics_aggregator_run_duration_seconds",
			Help:        "Wall-clock cost of one aggregation run (ingest + rollup)",
			ConstLabels: labels,
			Buckets:     []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}),
	}
	reg.MustRegister(
		m.ImpressionsLastWindow, m.PurchasesLastWindow,
		m.WalkoffsAtInitLastWindow, m.WalkoffsAtReserveLastWindow,
		m.GMVLastWindowEUR, m.ConversionRateLastWindow,
		m.RunsTotal, m.RunDurationSeconds,
	)
	m.handler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return m
}

func (m *Metrics) Handler() http.Handler { return m.handler }
