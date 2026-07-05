package rollup

import (
	"sort"
	"strings"
)

// LabelName is one of the label dimensions the aggregator can bucket
// by. Kept as constants so the CLI flag validator + the map access
// share the same source of truth.
type LabelName string

const (
	LabelExperiment   LabelName = "experiment"
	LabelVariant      LabelName = "variant"
	LabelCustomerTier LabelName = "customer_tier"
	LabelCountry      LabelName = "country"
)

// AllLabels enumerates every label the aggregator knows how to project.
var AllLabels = []LabelName{
	LabelExperiment,
	LabelVariant,
	LabelCustomerTier,
	LabelCountry,
}

// OtherBucket is the string every top-N-overflow label value collapses
// to. Chosen so Grafana filters can `label!="_other_"` cleanly.
const OtherBucket = "_other_"

// LabelSelection is the operator-configured opt-in subset. Zero-value
// means "aggregate everything into the env-only path" — no labeled
// gauges are emitted at all.
type LabelSelection struct {
	set map[LabelName]bool
}

// ParseLabelSelection accepts the operator's comma-separated flag value
// and returns a LabelSelection with only the enumerated names enabled.
// Unknown names are ignored (with an error so cmd can log/reject).
func ParseLabelSelection(csv string) (LabelSelection, error) {
	sel := LabelSelection{set: map[LabelName]bool{}}
	if strings.TrimSpace(csv) == "" {
		return sel, nil
	}
	valid := map[LabelName]bool{}
	for _, l := range AllLabels {
		valid[l] = true
	}
	for _, raw := range strings.Split(csv, ",") {
		name := LabelName(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if !valid[name] {
			return LabelSelection{}, &unknownLabelError{name: name}
		}
		sel.set[name] = true
	}
	return sel, nil
}

// Has reports whether the operator opted a specific dimension in.
func (s LabelSelection) Has(name LabelName) bool { return s.set[name] }

// Any reports whether the operator opted anything in. When false the
// labeled path is skipped entirely and only the env-only aggregates
// are emitted — zero per-row overhead beyond the total counters.
func (s LabelSelection) Any() bool { return len(s.set) > 0 }

// Names returns the opted-in label names in a stable order (matches
// AllLabels ordering) so Prometheus registration + `Values()` traversal
// agree.
func (s LabelSelection) Names() []LabelName {
	out := make([]LabelName, 0, len(s.set))
	for _, l := range AllLabels {
		if s.set[l] {
			out = append(out, l)
		}
	}
	return out
}

// LabelKey is the tuple that identifies one row in the labeled
// aggregation map. Only fields corresponding to opted-in dimensions are
// populated; the rest stay empty. Comparable by value — usable as a map
// key.
type LabelKey struct {
	Experiment   string
	Variant      string
	CustomerTier string
	Country      string
}

// Values returns the label values in the same order as sel.Names() so
// Prometheus GaugeVec.WithLabelValues() receives a matching slice.
func (k LabelKey) Values(sel LabelSelection) []string {
	out := make([]string, 0, 4)
	for _, name := range sel.Names() {
		switch name {
		case LabelExperiment:
			out = append(out, k.Experiment)
		case LabelVariant:
			out = append(out, k.Variant)
		case LabelCustomerTier:
			out = append(out, k.CustomerTier)
		case LabelCountry:
			out = append(out, k.Country)
		}
	}
	return out
}

// Bucket accumulates the per-window aggregates for one label tuple
// (or for the env-only total, when used unlabeled).
type Bucket struct {
	Impressions       int
	Purchases         int
	WalkoffsAtInit    int
	WalkoffsAtReserve int
	GMV               float64
}

// ConversionRate returns purchases / impressions, or 0 when there are
// no impressions.
func (b Bucket) ConversionRate() float64 {
	if b.Impressions == 0 {
		return 0
	}
	return float64(b.Purchases) / float64(b.Impressions)
}

func (b *Bucket) merge(o Bucket) {
	b.Impressions += o.Impressions
	b.Purchases += o.Purchases
	b.WalkoffsAtInit += o.WalkoffsAtInit
	b.WalkoffsAtReserve += o.WalkoffsAtReserve
	b.GMV += o.GMV
}

// TopN sorts labeled buckets by impressions descending, keeps up to
// `n` of them, and folds the rest into a single OtherBucket-labeled
// tuple. Returns the emit list in stable-sortable order.
//
// n <= 0 means "no cap" — every tuple is emitted verbatim. Deployments
// that trust their input to be bounded (small op-run environments)
// can set --top-n=0 to disable the collapse.
func TopN(labeled map[LabelKey]Bucket, sel LabelSelection, n int) []LabeledBucket {
	entries := make([]LabeledBucket, 0, len(labeled))
	for k, b := range labeled {
		entries = append(entries, LabeledBucket{Key: k, Bucket: b})
	}
	sort.Slice(entries, func(i, j int) bool {
		// Stable primary sort by impressions desc; break ties by a
		// deterministic label-order so successive windows don't shuffle
		// series that share an impression count.
		if entries[i].Bucket.Impressions != entries[j].Bucket.Impressions {
			return entries[i].Bucket.Impressions > entries[j].Bucket.Impressions
		}
		return keyLess(entries[i].Key, entries[j].Key)
	})
	if n <= 0 || len(entries) <= n {
		return entries
	}
	top := entries[:n]
	rest := entries[n:]
	other := LabeledBucket{
		Key: LabelKey{
			Experiment:   otherIfSelected(sel, LabelExperiment),
			Variant:      otherIfSelected(sel, LabelVariant),
			CustomerTier: otherIfSelected(sel, LabelCustomerTier),
			Country:      otherIfSelected(sel, LabelCountry),
		},
	}
	for _, r := range rest {
		other.Bucket.merge(r.Bucket)
	}
	// Only include the _other_ tuple when it actually carries mass;
	// with n=0 the loop above is empty and we skip.
	if other.Bucket.Impressions > 0 || other.Bucket.Purchases > 0 || other.Bucket.WalkoffsAtInit > 0 || other.Bucket.WalkoffsAtReserve > 0 || other.Bucket.GMV > 0 {
		top = append(top, other)
	}
	return top
}

func otherIfSelected(sel LabelSelection, name LabelName) string {
	if sel.Has(name) {
		return OtherBucket
	}
	return ""
}

func keyLess(a, b LabelKey) bool {
	if a.Experiment != b.Experiment {
		return a.Experiment < b.Experiment
	}
	if a.Variant != b.Variant {
		return a.Variant < b.Variant
	}
	if a.CustomerTier != b.CustomerTier {
		return a.CustomerTier < b.CustomerTier
	}
	return a.Country < b.Country
}

// LabeledBucket ties a label tuple to its bucket for emission.
type LabeledBucket struct {
	Key    LabelKey
	Bucket Bucket
}

type unknownLabelError struct{ name LabelName }

func (e *unknownLabelError) Error() string {
	valid := make([]string, 0, len(AllLabels))
	for _, l := range AllLabels {
		valid = append(valid, string(l))
	}
	return "rollup: unknown label " + string(e.name) + " (valid: " + strings.Join(valid, ", ") + ")"
}
