package rollup

import "testing"

func TestWindow_ZeroValueIsSafe(t *testing.T) {
	w := New(LabelSelection{})
	if w.ConversionRate() != 0 {
		t.Errorf("zero window conversion = %v, want 0", w.ConversionRate())
	}
}

func TestWindow_AddSearchCountsOffers(t *testing.T) {
	w := New(LabelSelection{})
	added := w.AddSearch([]byte(`{"offers":[{"a":1},{"b":2},{"c":3}]}`))
	if added != 3 || w.Impressions() != 3 {
		t.Errorf("added=%d Impressions=%d, want 3/3", added, w.Impressions())
	}
}

func TestWindow_AddSearchEmptyIsZero(t *testing.T) {
	w := New(LabelSelection{})
	added := w.AddSearch([]byte(`{"offers":[]}`))
	if added != 0 || w.Impressions() != 0 {
		t.Errorf("zero-offer search should not increment impressions; got %d/%d", added, w.Impressions())
	}
}

func TestWindow_AddBookingPurchase(t *testing.T) {
	w := New(LabelSelection{})
	kind := w.AddBooking([]byte(`{"event_type":"booking.purchased","revenue":49.99}`))
	if kind != "purchased" || w.Purchases() != 1 || w.GMV() != 49.99 {
		t.Errorf("kind=%q Purchases=%d GMV=%v", kind, w.Purchases(), w.GMV())
	}
}

func TestWindow_AddBookingTimeoutPhaseInitiated(t *testing.T) {
	w := New(LabelSelection{})
	kind := w.AddBooking([]byte(`{"event_type":"booking.timeout","phase":"initiated"}`))
	if kind != "walkoff_init" || w.WalkoffsAtInit() != 1 {
		t.Errorf("kind=%q WalkoffsAtInit=%d", kind, w.WalkoffsAtInit())
	}
}

func TestWindow_AddBookingTimeoutPhaseReserved(t *testing.T) {
	w := New(LabelSelection{})
	kind := w.AddBooking([]byte(`{"event_type":"booking.timeout","phase":"reserved"}`))
	if kind != "walkoff_reserve" || w.WalkoffsAtReserve() != 1 {
		t.Errorf("kind=%q WalkoffsAtReserve=%d", kind, w.WalkoffsAtReserve())
	}
}

func TestWindow_AddBookingIgnoresOtherEventTypes(t *testing.T) {
	w := New(LabelSelection{})
	kind := w.AddBooking([]byte(`{"event_type":"booking.initiated"}`))
	if kind != "" || w.Purchases() != 0 {
		t.Errorf("initiated must not affect purchases; got kind=%q", kind)
	}
}

func TestWindow_ConversionRate(t *testing.T) {
	w := New(LabelSelection{})
	w.Total.Impressions = 100
	w.Total.Purchases = 25
	if got := w.ConversionRate(); got != 0.25 {
		t.Errorf("conversion = %v, want 0.25", got)
	}
}

func TestWindow_LabeledAccumulatesPerTuple(t *testing.T) {
	sel, err := ParseLabelSelection("variant,customer_tier")
	if err != nil {
		t.Fatal(err)
	}
	w := New(sel)
	w.AddSearch([]byte(`{
		"query":{"customer_tier":"gold","country":"DE"},
		"offers":[
			{"variant":"control","experiment":"exp1"},
			{"variant":"treatment","experiment":"exp1"},
			{"variant":"control","experiment":"exp1"}
		]
	}`))
	if w.Impressions() != 3 {
		t.Fatalf("total impressions = %d, want 3", w.Impressions())
	}
	control := w.Labeled[LabelKey{Variant: "control", CustomerTier: "gold"}]
	treatment := w.Labeled[LabelKey{Variant: "treatment", CustomerTier: "gold"}]
	if control.Impressions != 2 || treatment.Impressions != 1 {
		t.Errorf("labeled tuples wrong: control=%d treatment=%d", control.Impressions, treatment.Impressions)
	}
}

func TestWindow_LabeledNotPopulatedWhenSelectionEmpty(t *testing.T) {
	w := New(LabelSelection{})
	w.AddSearch([]byte(`{"query":{"customer_tier":"gold"},"offers":[{"variant":"a"}]}`))
	if w.Labeled != nil {
		t.Errorf("Labeled should be nil when no selection; got %v", w.Labeled)
	}
}

func TestParseLabelSelection_Valid(t *testing.T) {
	sel, err := ParseLabelSelection("variant,customer_tier")
	if err != nil {
		t.Fatal(err)
	}
	if !sel.Has(LabelVariant) || !sel.Has(LabelCustomerTier) {
		t.Errorf("selection missing expected labels")
	}
	if sel.Has(LabelExperiment) || sel.Has(LabelCountry) {
		t.Errorf("selection has unexpected labels")
	}
}

func TestParseLabelSelection_Empty(t *testing.T) {
	sel, err := ParseLabelSelection("")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Any() {
		t.Errorf("empty CSV should yield empty selection; sel.Any()=true")
	}
}

func TestParseLabelSelection_UnknownRejects(t *testing.T) {
	_, err := ParseLabelSelection("variant,unknown_label")
	if err == nil {
		t.Errorf("unknown label should be rejected")
	}
}

func TestParseLabelSelection_NamesStableOrder(t *testing.T) {
	sel, _ := ParseLabelSelection("country,customer_tier,variant,experiment")
	names := sel.Names()
	want := []LabelName{LabelExperiment, LabelVariant, LabelCustomerTier, LabelCountry}
	if len(names) != len(want) {
		t.Fatalf("names len = %d, want %d", len(names), len(want))
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestTopN_UnderCap(t *testing.T) {
	sel, _ := ParseLabelSelection("variant")
	labeled := map[LabelKey]Bucket{
		{Variant: "a"}: {Impressions: 10, Purchases: 1},
		{Variant: "b"}: {Impressions: 5, Purchases: 0},
	}
	got := TopN(labeled, sel, 5)
	if len(got) != 2 {
		t.Errorf("under-cap should not add _other_; got %d entries", len(got))
	}
}

func TestTopN_OverCapFoldsRestIntoOther(t *testing.T) {
	sel, _ := ParseLabelSelection("variant")
	labeled := map[LabelKey]Bucket{
		{Variant: "top1"}: {Impressions: 100},
		{Variant: "top2"}: {Impressions: 80},
		{Variant: "top3"}: {Impressions: 40},
		{Variant: "low1"}: {Impressions: 10, Purchases: 1},
		{Variant: "low2"}: {Impressions: 5, GMV: 20.0},
	}
	got := TopN(labeled, sel, 3)
	if len(got) != 4 {
		t.Fatalf("want 3 top + 1 other = 4; got %d", len(got))
	}
	other := got[3]
	if other.Key.Variant != OtherBucket {
		t.Errorf("last entry variant = %q, want %q", other.Key.Variant, OtherBucket)
	}
	if other.Bucket.Impressions != 15 || other.Bucket.Purchases != 1 || other.Bucket.GMV != 20.0 {
		t.Errorf("_other_ sums wrong: %+v", other.Bucket)
	}
}

func TestTopN_ZeroCapMeansNoLimit(t *testing.T) {
	sel, _ := ParseLabelSelection("variant")
	labeled := map[LabelKey]Bucket{
		{Variant: "a"}: {Impressions: 3},
		{Variant: "b"}: {Impressions: 2},
		{Variant: "c"}: {Impressions: 1},
	}
	got := TopN(labeled, sel, 0)
	if len(got) != 3 {
		t.Errorf("n=0 should emit all; got %d", len(got))
	}
	for _, e := range got {
		if e.Key.Variant == OtherBucket {
			t.Errorf("n=0 should never emit _other_; got %+v", e)
		}
	}
}

func TestBucket_ConversionRate(t *testing.T) {
	b := Bucket{Impressions: 200, Purchases: 40}
	if got := b.ConversionRate(); got != 0.20 {
		t.Errorf("conversion = %v, want 0.20", got)
	}
}
