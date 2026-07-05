# Architecture Decision Records

Each Markdown file records one architectural decision. New records are appended with the next four-digit prefix and referenced from this index. `scripts/check-adrs.sh` (wired into `make ci-local`) verifies every ADR appears here and every linked ADR file exists.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-role.md) | Role of pricing-metrics-aggregator in the platform | ✅ Accepted |
| [0002](0002-metric-cardinality-caps.md) | Metric cardinality caps + per-arm / per-segment gauges | ✅ Accepted |
