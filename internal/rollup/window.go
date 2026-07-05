// Package rollup computes per-window aggregates over the raw event
// streams. v0.0.1 kept cardinality to a single {env} label; v0.0.2 adds
// operator-configurable per-label buckets with top-N cardinality caps
// per ADR-0003.
package rollup

import (
	"encoding/json"
)

// Window carries the per-window state. The Total bucket is always
// populated (feeds env-only gauges); Labeled is populated only when the
// LabelSelection opts anything in (feeds per-tuple gauges).
type Window struct {
	Total   Bucket
	Labeled map[LabelKey]Bucket
	sel     LabelSelection
}

// New constructs a Window ready to accumulate. Passing a zero-value
// LabelSelection yields the env-only path (no labeled state kept).
func New(sel LabelSelection) *Window {
	w := &Window{sel: sel}
	if sel.Any() {
		w.Labeled = map[LabelKey]Bucket{}
	}
	return w
}

// ConversionRate on the total bucket, exposed at the Window level for
// backward compatibility with v0.0.1 callers that read w.ConversionRate().
func (w *Window) ConversionRate() float64 { return w.Total.ConversionRate() }

// Impressions on the total, kept as a struct-field style getter so
// v0.0.1 callers that read w.Impressions keep compiling if we swap
// them to method-based access later.
func (w *Window) Impressions() int       { return w.Total.Impressions }
func (w *Window) Purchases() int         { return w.Total.Purchases }
func (w *Window) WalkoffsAtInit() int    { return w.Total.WalkoffsAtInit }
func (w *Window) WalkoffsAtReserve() int { return w.Total.WalkoffsAtReserve }
func (w *Window) GMV() float64           { return w.Total.GMV }

// AddSearch handles one search.v1 row. Every offer in the offers[]
// array counts as an impression. When labels are opted in, the offer's
// (experiment, variant) plus the query's (customer_tier, country) form
// the tuple key. Offers can differ per-row on (experiment, variant)
// even inside one search event, so per-offer keys are the honest
// granularity.
func (w *Window) AddSearch(row []byte) int {
	var e struct {
		Query struct {
			CustomerTier string `json:"customer_tier"`
			Country      string `json:"country"`
		} `json:"query"`
		Offers []struct {
			Experiment string `json:"experiment"`
			Variant    string `json:"variant"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(row, &e); err != nil {
		return 0
	}
	added := len(e.Offers)
	w.Total.Impressions += added
	if !w.sel.Any() {
		return added
	}
	for _, o := range e.Offers {
		k := w.keyFor(o.Experiment, o.Variant, e.Query.CustomerTier, e.Query.Country)
		b := w.Labeled[k]
		b.Impressions++
		w.Labeled[k] = b
	}
	return added
}

// AddBooking dispatches on event_type and updates the appropriate
// counters + labeled tuples. Labels come from the top-level fields on
// booking.v1 (customer_tier / country are not stamped on booking events
// — they would have to be joined from search — so labeled buckets on
// the booking side use only experiment + variant).
func (w *Window) AddBooking(row []byte) string {
	var e struct {
		EventType  string  `json:"event_type"`
		Revenue    float64 `json:"revenue"`
		Phase      string  `json:"phase"`
		Experiment string  `json:"experiment"`
		Variant    string  `json:"variant"`
	}
	if err := json.Unmarshal(row, &e); err != nil {
		return ""
	}
	switch e.EventType {
	case "booking.purchased":
		w.Total.Purchases++
		w.Total.GMV += e.Revenue
		if w.sel.Any() {
			k := w.keyFor(e.Experiment, e.Variant, "", "")
			b := w.Labeled[k]
			b.Purchases++
			b.GMV += e.Revenue
			w.Labeled[k] = b
		}
		return "purchased"
	case "booking.timeout":
		if e.Phase == "initiated" {
			w.Total.WalkoffsAtInit++
			if w.sel.Any() {
				k := w.keyFor(e.Experiment, e.Variant, "", "")
				b := w.Labeled[k]
				b.WalkoffsAtInit++
				w.Labeled[k] = b
			}
			return "walkoff_init"
		}
		if e.Phase == "reserved" {
			w.Total.WalkoffsAtReserve++
			if w.sel.Any() {
				k := w.keyFor(e.Experiment, e.Variant, "", "")
				b := w.Labeled[k]
				b.WalkoffsAtReserve++
				w.Labeled[k] = b
			}
			return "walkoff_reserve"
		}
	}
	return ""
}

// keyFor builds a LabelKey with only the opted-in dimensions populated.
// Non-opted-in dimensions stay empty so the map's cardinality is
// bounded by the product of unique values across opted-in labels.
func (w *Window) keyFor(experiment, variant, customerTier, country string) LabelKey {
	var k LabelKey
	if w.sel.Has(LabelExperiment) {
		k.Experiment = experiment
	}
	if w.sel.Has(LabelVariant) {
		k.Variant = variant
	}
	if w.sel.Has(LabelCustomerTier) {
		k.CustomerTier = customerTier
	}
	if w.sel.Has(LabelCountry) {
		k.Country = country
	}
	return k
}

// LabelSelection reads back the operator's opt-in set so cmd + emit
// paths share one source of truth.
func (w *Window) LabelSelection() LabelSelection { return w.sel }
