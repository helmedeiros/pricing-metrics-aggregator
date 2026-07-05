# Changelog

All notable changes to this project are documented in this file.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) + [SemVer](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- ADR-0002 Accepted — metric cardinality caps + per-arm / per-segment gauges. `--include-labels=experiment,variant,customer_tier,country` (any subset) turns on parallel `pricing_*_last_window_labeled` gauges registered dynamically from the operator's opted-in dimensions. `--top-n=50` (default) sorts observed tuples by impressions descending and folds the tail into a single `_other_` bucket so cardinality is bounded regardless of upstream traffic mix. `--top-n=0` disables the cap. The v0.0.1 env-only `pricing_*_last_window` surface stays bit-for-bit unchanged; dashboards using it need no migration. Booking-side segment attribution (per-customer_tier purchases / walkoffs / GMV) requires joining `search.v1` on `decision_id`; deferred per ADR-0002 Not-closed. Verified end-to-end: two traffic-gen sessions at different `--session-customer-tier` values produced distinct `customer_tier="enterprise"` and `customer_tier="consumer"` series on the labeled impression gauges; totals matched v0.0.1's env-only path.
- Source-level break: `prom.New(env, sel)` grew a second parameter (was `prom.New(env)` in v0.0.1). No wire-level fallout; the aggregator is a single-binary program with no external callers.

### Added (v0.0.1)

- ADR-0001 Accepted — Role of pricing-metrics-aggregator. New service that polls the funnel-events MinIO bucket on a rolling window, computes per-window aggregates over `search.v1` + `booking.v1`, publishes to Prometheus as last-window gauges. Feeds Grafana + AlertManager. Notebooks (pricing-observability ADR-0021) remain the on-demand analytical surface; this service is the always-on continuous one. Single `{env}` cardinality in v0.0.1 — per-arm / per-segment / per-rule breakdowns land in ADR-0003 (cardinality caps, parked with a concrete trigger).
- Initial scaffold: `cmd/pricing-metrics-aggregator/main.go`, `internal/ingest/` (minio-go SDK, Hive-partition-aware walk over dt=/hour=/env= prefixes), `internal/rollup/` (per-window aggregate state with per-row search / booking dispatchers), `internal/observability/metrics/prom/` (six last-window gauges + runs-total counter + run-duration histogram), `internal/httpapi/health.go` (Healthz / Readyz), `internal/jsonlog/` (platform-shape structured logs). Standard Go service posture: distroless static:nonroot image, multi-arch buildx, `make ci-local` gates.
- End-to-end verification against the live compose stack: a fresh traffic-gen `--session=4` over 8s drove 28 searches (84 impressions), 10 purchases (11.9% conversion), 9 walkoffs-at-init, 5 walkoffs-at-reserve, €586.38 GMV — every last-window gauge tracked the traffic-gen session stats correctly.
