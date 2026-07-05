# pricing-metrics-aggregator

The Pricing Metrics Aggregator container from the Pricing Decision Platform's C4 diagram. Polls the funnel-events MinIO bucket on a rolling window (5 minutes default), computes aggregates over `search.v1` + `booking.v1`, publishes them to Prometheus as last-window gauges. Feeds Grafana dashboards and AlertManager rules downstream.

See [ADR-0001](docs/architecture/decisions/0001-role.md) for the full role framing, the alternatives considered, and the concrete triggers for the parked ADRs (0002 window shape, 0003 cardinality caps, 0004 Parquet rollup, 0005 exposition rules).

## Metric surface

| Metric | Type | Meaning |
|---|---|---|
| `pricing_impressions_last_window{env}` | gauge | Priced offers shown in the window (search.v1 offers[] length) |
| `pricing_purchases_last_window{env}` | gauge | `booking.purchased` events in the window |
| `pricing_walkoffs_at_init_last_window{env}` | gauge | `booking.timeout{phase=initiated}` — price-shock cohort |
| `pricing_walkoffs_at_reserve_last_window{env}` | gauge | `booking.timeout{phase=reserved}` — payment-friction cohort |
| `pricing_gmv_last_window_eur{env}` | gauge | Sum of `booking.purchased.revenue` in the window |
| `pricing_conversion_rate_last_window{env}` | gauge | purchases / impressions (0 when no impressions) |
| `pricing_metrics_aggregator_runs_total{env,outcome}` | counter | Aggregation runs completed, keyed by `ok` / `error` |
| `pricing_metrics_aggregator_run_duration_seconds{env}` | histogram | Wall-clock of one ingest + rollup pass |

Cardinality is intentionally minimal in v0.0.1 (single `{env}` label). Per-arm and per-segment breakdowns land after ADR-0003 (cardinality caps) — see the ADR-0001 Not-closed list.

## Quick start (compose)

`decision-gateway/docker-compose.yaml` includes `pricing-metrics-aggregator` alongside markup-svc, funnel-sim, and minio. From the decision-gateway repo:

```bash
docker compose up pricing-metrics-aggregator
# Once it's up:
curl -sS http://localhost:8082/metrics | grep '^pricing_'
```

## CLI flags

| Flag | Default | Purpose |
|---|---|---|
| `--listen` | `:8082` | HTTP listen for /metrics + probes |
| `--env` | `default` | Environment label stamped on every metric |
| `--bucket` | `funnel-events` | MinIO bucket to poll |
| `--event-source-endpoint` | `minio:9000` | S3 endpoint host:port |
| `--event-source-region` | `us-east-1` | S3 region |
| `--event-source-access-key` | `$AWS_ACCESS_KEY_ID` | S3 access key |
| `--event-source-secret-key` | `$AWS_SECRET_ACCESS_KEY` | S3 secret key |
| `--event-source-use-ssl` | `false` | HTTPS to the S3 endpoint |
| `--interval` | `5m` | Aggregation interval (window is tumbling, size = interval) |
| `--warmup` | `5s` | Startup grace before the first tick |
| `--otel-enabled` | `false` | Bootstrap OTel SDK + OTLP gRPC export |

## Quality gates

`make ci-local` runs `go vet`, race-detector tests, the coverage gate (`COVER_MIN`), and the ADR-index check. Multi-arch image (`linux/amd64` + `linux/arm64`) publishes to `ghcr.io/helmedeiros/pricing-metrics-aggregator` on main and tag pushes.

## License

[MIT](LICENSE)
