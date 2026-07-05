// Package rollup computes per-window aggregates over the raw event
// streams. v0.0.1 keeps cardinality to a single {env} label — per-arm
// and per-segment aggregates land in a follow-on ADR after the
// cardinality-caps design is locked (see ADR-0003 parked).
package rollup

import (
	"encoding/json"
)

// Window is the result of one aggregation pass over the buckets. Zero
// value is a valid empty window (all counters zero) — consumers must
// handle "no events in window" as a legal state.
type Window struct {
	Impressions        int
	Purchases          int
	WalkoffsAtInit     int
	WalkoffsAtReserve  int
	GMV                float64
}

// ConversionRate = purchases / impressions. Returns 0 when there are
// no impressions (undefined mathematically; zero is honest — nothing
// to convert).
func (w Window) ConversionRate() float64 {
	if w.Impressions == 0 {
		return 0
	}
	return float64(w.Purchases) / float64(w.Impressions)
}

// searchEvent is the minimal shape we need from a search.v1 row —
// only the offers array is consumed for the impression count. A search
// event with an empty offers[] produces zero impressions (a zero-offer
// search happens when every /decide fan-out failed).
type searchEvent struct {
	Offers []struct{} `json:"offers"`
}

// AddSearch increments the impression count by len(offers) on the
// event. Returns the number of impressions added.
func (w *Window) AddSearch(row []byte) int {
	var e searchEvent
	if err := json.Unmarshal(row, &e); err != nil {
		return 0
	}
	w.Impressions += len(e.Offers)
	return len(e.Offers)
}

// bookingEvent captures only what the aggregator needs from a
// booking.v1 row: the event_type discriminator, revenue on purchases,
// and phase on timeouts.
type bookingEvent struct {
	EventType string  `json:"event_type"`
	Revenue   float64 `json:"revenue"`
	Phase     string  `json:"phase"`
}

// AddBooking dispatches on event_type. Returns the field the row
// affected ("purchased" | "walkoff_init" | "walkoff_reserve" | "").
func (w *Window) AddBooking(row []byte) string {
	var e bookingEvent
	if err := json.Unmarshal(row, &e); err != nil {
		return ""
	}
	switch e.EventType {
	case "booking.purchased":
		w.Purchases++
		w.GMV += e.Revenue
		return "purchased"
	case "booking.timeout":
		if e.Phase == "initiated" {
			w.WalkoffsAtInit++
			return "walkoff_init"
		}
		if e.Phase == "reserved" {
			w.WalkoffsAtReserve++
			return "walkoff_reserve"
		}
	}
	return ""
}
