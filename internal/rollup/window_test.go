package rollup

import "testing"

func TestWindow_ZeroValueIsSafe(t *testing.T) {
	var w Window
	if w.ConversionRate() != 0 {
		t.Errorf("zero window conversion = %v, want 0", w.ConversionRate())
	}
}

func TestWindow_AddSearchCountsOffers(t *testing.T) {
	w := &Window{}
	added := w.AddSearch([]byte(`{"offers":[{"a":1},{"b":2},{"c":3}]}`))
	if added != 3 || w.Impressions != 3 {
		t.Errorf("added=%d Impressions=%d, want 3/3", added, w.Impressions)
	}
}

func TestWindow_AddSearchEmptyIsZero(t *testing.T) {
	w := &Window{}
	added := w.AddSearch([]byte(`{"offers":[]}`))
	if added != 0 || w.Impressions != 0 {
		t.Errorf("zero-offer search should not increment impressions; got %d/%d", added, w.Impressions)
	}
}

func TestWindow_AddBookingPurchase(t *testing.T) {
	w := &Window{}
	kind := w.AddBooking([]byte(`{"event_type":"booking.purchased","revenue":49.99}`))
	if kind != "purchased" || w.Purchases != 1 || w.GMV != 49.99 {
		t.Errorf("kind=%q Purchases=%d GMV=%v", kind, w.Purchases, w.GMV)
	}
}

func TestWindow_AddBookingTimeoutPhaseInitiated(t *testing.T) {
	w := &Window{}
	kind := w.AddBooking([]byte(`{"event_type":"booking.timeout","phase":"initiated"}`))
	if kind != "walkoff_init" || w.WalkoffsAtInit != 1 {
		t.Errorf("kind=%q WalkoffsAtInit=%d", kind, w.WalkoffsAtInit)
	}
}

func TestWindow_AddBookingTimeoutPhaseReserved(t *testing.T) {
	w := &Window{}
	kind := w.AddBooking([]byte(`{"event_type":"booking.timeout","phase":"reserved"}`))
	if kind != "walkoff_reserve" || w.WalkoffsAtReserve != 1 {
		t.Errorf("kind=%q WalkoffsAtReserve=%d", kind, w.WalkoffsAtReserve)
	}
}

func TestWindow_AddBookingIgnoresOtherEventTypes(t *testing.T) {
	w := &Window{}
	kind := w.AddBooking([]byte(`{"event_type":"booking.initiated"}`))
	if kind != "" || w.Purchases != 0 || w.WalkoffsAtInit != 0 {
		t.Errorf("initiated must not affect purchases/walkoffs; got kind=%q", kind)
	}
}

func TestWindow_ConversionRate(t *testing.T) {
	w := Window{Impressions: 100, Purchases: 25}
	got := w.ConversionRate()
	if got != 0.25 {
		t.Errorf("conversion = %v, want 0.25", got)
	}
}
