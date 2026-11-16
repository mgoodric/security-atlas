package scfrelease

// Kind says what became of one SCF code from an earlier release in a later
// one. It is read from the later release's legacy SCF IDs (#1769: each
// anchor's code in the release before) and titles.
type Kind string

const (
	// KindUnchanged: the later release has the same code, from the same
	// lineage, with the same title.
	KindUnchanged Kind = "unchanged"
	// KindRenamed: same code and lineage, different title.
	KindRenamed Kind = "renamed"
	// KindSplit: two or more later anchors name this code as their legacy
	// code, so the control was divided.
	KindSplit Kind = "split"
	// KindReused: the later release has the same code, but its legacy code
	// says it came from a different earlier control.
	KindReused Kind = "reused"
	// KindRenumbered: the code is gone, and exactly one later anchor names
	// it as its legacy code.
	KindRenumbered Kind = "renumbered"
	// KindWithdrawn: the code is gone and nothing later descends from it.
	KindWithdrawn Kind = "withdrawn"
)

// Changed reports whether an anchor bound by this code in the earlier
// release means something different in the later one.
func (k Kind) Changed() bool { return k != KindUnchanged }

// Anchor is the part of a catalog anchor the classifier reads. LegacyCode
// is empty when the catalog does not say, which counts as the same
// lineage.
type Anchor struct {
	Code       string
	Title      string
	LegacyCode string
}

// Change is one code's classification, with the later anchor it was read
// from (empty Code when the code is gone from the later release).
type Change struct {
	Kind  Kind
	From  Anchor
	Later Anchor
}

// Classify says what became of each code of the earlier release in the
// later one. The result has one entry per code in earlier.
func Classify(earlier, later []Anchor) map[string]Change {
	byCode := make(map[string]Anchor, len(later))
	successors := make(map[string]int, len(later))
	for _, a := range later {
		byCode[a.Code] = a
		if a.LegacyCode != "" {
			successors[a.LegacyCode]++
		}
	}
	out := make(map[string]Change, len(earlier))
	for _, a := range earlier {
		same, present := byCode[a.Code]
		c := Change{From: a, Later: same}
		switch {
		case present && same.LegacyCode != "" && same.LegacyCode != a.Code:
			c.Kind = KindReused
		case successors[a.Code] >= 2:
			c.Kind = KindSplit
		case !present && successors[a.Code] == 1:
			c.Kind = KindRenumbered
		case !present:
			c.Kind = KindWithdrawn
		case same.Title != a.Title:
			c.Kind = KindRenamed
		default:
			c.Kind = KindUnchanged
		}
		out[a.Code] = c
	}
	return out
}
