# Changelog

All notable changes to this project are documented in this file.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) + [SemVer](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- ADR-0001 Accepted — Role of pricing-metrics-aggregator. New service that polls the funnel-events MinIO bucket on a rolling window, computes per-window aggregates over `search.v1` + `booking.v1`, publishes to Prometheus as last-window gauges. Feeds Grafana + AlertManager. Notebooks (pricing-observability ADR-0021) remain the on-demand analytical surface; this service is the always-on continuous one. Single `{env}` cardinality in v0.0.1 — per-arm / per-segment / per-rule breakdowns land in ADR-0003 (cardinality caps, parked with a concrete trigger).
- Initial scaffold: `cmd/pricing-metrics-aggregator/main.go`, `internal/ingest/` (minio-go SDK, Hive-partition-aware walk over dt=/hour=/env= prefixes), `internal/rollup/` (per-window aggregate state with per-row search / booking dispatchers), `internal/observability/metrics/prom/` (six last-window gauges + runs-total counter + run-duration histogram), `internal/httpapi/health.go` (Healthz / Readyz), `internal/jsonlog/` (platform-shape structured logs). Standard Go service posture: distroless static:nonroot image, multi-arch buildx, `make ci-local` gates.
- End-to-end verification against the live compose stack: a fresh traffic-gen `--session=4` over 8s drove 28 searches (84 impressions), 10 purchases (11.9% conversion), 9 walkoffs-at-init, 5 walkoffs-at-reserve, €586.38 GMV — every last-window gauge tracked the traffic-gen session stats correctly.
