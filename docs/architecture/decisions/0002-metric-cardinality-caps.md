# 2. Metric cardinality caps + per-arm / per-segment gauges

## Status

Accepted — pricing-metrics-aggregator v0.0.2 lands operator-configurable label projection (`--include-labels`) over the four dimensions the funnel events carry (`experiment`, `variant`, `customer_tier`, `country`), with top-N-by-impressions bucketing at emission time so cardinality stays bounded. The env-only `pricing_*_last_window` gauge surface from v0.0.1 stays unchanged; the new labeled gauges land as parallel `pricing_*_last_window_labeled` metrics registered dynamically at boot from the operator's label subset.

This ADR was numbered `0003` in the aggregator's initial ADR-0001 Not-closed list but ships as `0002` here — the window-shape ADR originally planned for `0002` is renumbered `0003` in a follow-on.

## Context

pricing-metrics-aggregator v0.0.1 (ADR-0001) shipped six `pricing_*_last_window` gauges labeled by `{env}` only. That surface answers "what's happening on the platform right now" but not "what's happening on the treatment arm vs. control right now" or "what's happening in the DE gold-tier segment vs. baseline right now." Both questions are load-bearing for Grafana panels asking about lift and elasticity in real time.

Adding labels naively (six `pricing_*` gauges × 4 label dimensions × ~500 unique combinations at typical traffic ≈ ~12,000 histogram-style series) blows the Prometheus scrape budget on a dev-scale MinIO. Any cardinality-adding metric needs a cap.

Two orthogonal design questions:

1. **Which labels?** The events carry different subsets. `search.v1.query` has `customer_tier` + `country` + `channel` + `device`; `search.v1.offers[]` has `experiment` + `variant`. `booking.v1` top-level has `experiment` + `variant` but NOT `customer_tier` / `country` (those would require joining back to `search.v1` on `decision_id`).
2. **How much cardinality?** Even opting into a single label — say `customer_tier` — can produce dozens of tuples over a busy window. And an operator new to the surface will underestimate.

## Decision

### `--include-labels` — operator opts into label dimensions

New CLI flag: `--include-labels=experiment,variant,customer_tier,country` (any subset, comma-separated). Empty (default) preserves the v0.0.1 env-only surface bit-for-bit — labeled gauges are neither registered nor emitted.

Unknown label names fail the boot with a clear error listing the valid set.

### Metric name split — parallel `_labeled` surface

Prometheus metric labels are fixed at registration. Rather than mutate the existing metric names (would force every Grafana dashboard + AlertManager rule using them to migrate at once), v0.0.2 adds a parallel set with a `_labeled` suffix:

```
pricing_impressions_last_window                              env-only, from v0.0.1
pricing_impressions_last_window_labeled{env, <opted-in>}     new in v0.0.2
```

Same pattern for `purchases`, `walkoffs_at_init`, `walkoffs_at_reserve`, `gmv_eur`, `conversion_rate`. Operators using the v0.0.1 dashboards see no change; operators building per-arm dashboards use `_labeled`.

### Label projection — from the event that carries the label

- **`experiment` + `variant`** — stamped on both `search.v1.offers[]` and `booking.v1` per funnel-sim ADR-0002 / ADR-0003. Fully attributable across impressions, purchases, walkoffs, GMV.
- **`customer_tier` + `country`** — stamped on `search.v1.query` only. `booking.v1` does NOT carry them (booking events would need to be joined to their originating search event on `decision_id` to reconstruct customer_tier).

The aggregator projects labels from the event that stamps them. Impressions (search) get all four opted-in labels; purchases and walkoffs (booking) get only `experiment` + `variant` even when the operator opted `customer_tier` + `country` in. Booking metrics on the labeled surface with a segment label opted in will appear under the empty-string label value for the un-attributed dimensions — grep-friendly and honest, but a Grafana panel showing "conversion by customer_tier" needs the search-side join to get purchase attribution.

The join is out of scope for v0.0.2 — see Not-closed.

### Top-N bucketing at emit time

New CLI flag: `--top-n=50` (default). Every window, after ingest, the labeled buckets are sorted by impressions descending; the top N are emitted as their own series; the rest sum into a single `_other_`-valued tuple (every opted-in dimension gets the string `_other_`).

Set `--top-n=0` to disable the cap — every observed tuple emits as its own series. Deployments in bounded-cardinality environments (small dev, single-tier operator runs) can opt into this. Never suitable for production traffic without external cardinality management.

Prometheus GaugeVec has no built-in "delete series that fell out of top-N" hook, so every window `Reset()`s every labeled GaugeVec before setting the top-N. A tuple that drops out of the top-N this window disappears from `/metrics` next scrape (correct behavior; not stale).

### Tie-breaking is deterministic

Two tuples with equal impressions break ties on lexicographic `(experiment, variant, customer_tier, country)` order. Consecutive windows with the same input produce the same series set — Grafana panels do not shuffle.

### Backward compatibility

v0.0.1 callers of `prom.New(env)` (positional single-arg) no longer compile — the signature grew a second `rollup.LabelSelection` parameter. Zero external callers today (the aggregator is a single-binary program), so this is a source break with no wire-level fallout. Documented for the CHANGELOG.

## Consequences

### Positive
- **Per-arm Grafana panels unblocked.** A `--include-labels=experiment,variant` + Grafana query `pricing_conversion_rate_last_window_labeled{variant="treatment"}` gives arm-level lift in real time. This is the primary reason ADR-0001 flagged this ADR as the first follow-on.
- **Per-segment impression view.** `--include-labels=customer_tier,country` on the search-side impression + walkoff metrics answers "which segments are seeing our offers".
- **v0.0.1 dashboards keep working.** The `_labeled` suffix split preserves the existing metric surface bit-for-bit.
- **Cardinality is bounded.** `--top-n=50` × 4 label dimensions × 6 metric names = 300-series ceiling per env, regardless of upstream traffic mix.

### Negative
- **Booking-side segment attribution requires a join.** `customer_tier` and `country` on purchase / walkoff metrics can't be reconstructed from booking events alone. Grafana panels using segment labels on purchase metrics see the `""` empty-label value carrying all the mass. Non-blocking for arm-based analysis; blocking for segment-based conversion analysis.
- **`_labeled` surface doubles the metric name count.** Panel authors need to know when to use the total vs. the labeled — documented in the aggregator's README.
- **`Reset()` per tick is not free.** Six `GaugeVec.Reset()` calls per aggregation window at 1-minute intervals is ~360 opns/hour — negligible compared to the ingest + JSON decode cost, but on the record for future perf tuning.

### Not closed (deferred to follow-on ADRs)

- **Cross-stream join for booking-side segment attribution.** When the operator wants per-segment purchase / walkoff / GMV attribution, the aggregator has to load `search.v1` first, index by `decision_id`, and enrich each `booking.v1` row with the search's customer_tier + country. This is a substantially bigger design — mostly around memory bounds (an in-memory join over a 24-hour window at 500 QPS is ~10 GB) and staleness (a booking whose search fell out of the window has no join key). Deferred until per-segment purchase analytics moves off the DuckDB notebooks and into Grafana.
- **`rule` label.** `rule` is on `markup.decision.v1`, not on funnel-events. Attributing conversion per rule requires walking a third bucket + joining. Same as the segment attribution join above — deferred.
- **Additional segment labels.** `channel`, `device`, `route`, `passengers` are also on `search.v1.query` and could be added to the opt-in set. Held until an operator use case appears — every added dimension multiplies the cardinality space.
- **Percentile bucketing instead of top-N.** For high-cardinality tail distributions (~10k unique tuples), top-N drops the entire long tail into one `_other_` bucket, which loses tail signal. A percentile-based bucketing (top-90th, next-95th, remainder) preserves more. Trigger: when a Grafana panel author flags that `_other_` is masking a signal they care about.
- **Alerting on `_other_` mass.** A sudden spike in the `_other_` bucket means the label distribution shifted — a real signal but currently invisible. `pricing_metrics_aggregator_other_share` gauge would surface it.

## References

- pricing-metrics-aggregator ADR-0001 — role framing that flagged this ADR as parked.
- funnel-sim ADR-0002 — `search.v1` schema (source of impression labels).
- funnel-sim ADR-0003 — `booking.v1` schema (source of booking labels; documents the missing `customer_tier` / `country`).
- pricing-observability ADR-0020 — cross-repo correlation contract; the join key this ADR's follow-on would use.
