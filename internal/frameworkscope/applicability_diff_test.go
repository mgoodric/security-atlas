package frameworkscope

import (
	"testing"

	"github.com/google/uuid"
)

func TestDiffDecisions(t *testing.T) {
	t.Parallel()
	ids := make([]uuid.UUID, 7)
	for i := range ids {
		ids[i] = uuid.New()
	}
	dec := func(d, j, ref string) *Decision {
		return &Decision{Decision: d, Justification: j, ImplementationRef: ref}
	}
	req := func(i int, d *Decision) RequirementApplicability {
		return RequirementApplicability{RequirementID: ids[i], Code: string(rune('A' + i)), Decision: d}
	}
	base := []RequirementApplicability{
		req(0, nil),                             // undecided both sides
		req(1, nil),                             // added
		req(2, dec("applicable", "", "")),       // removed
		req(3, dec("excluded", "no dev", "")),   // decision changed
		req(4, dec("excluded", "no dev", "")),   // justification changed
		req(5, dec("applicable", "", "wiki/a")), // implementation_ref only: unchanged
		req(6, dec("excluded", "no card", "")),  // unchanged
	}
	target := []RequirementApplicability{
		req(0, nil),
		req(1, dec("applicable", "", "")),
		req(2, nil),
		req(3, dec("applicable", "", "")),
		req(4, dec("excluded", "no in-house dev", "")),
		req(5, dec("applicable", "", "wiki/b")),
		req(6, dec("excluded", "no card", "")),
	}
	got := DiffDecisions(base, target)
	want := []struct {
		i    int
		kind string
	}{{1, ChangeAdded}, {2, ChangeRemoved}, {3, ChangeDecisionChanged}, {4, ChangeJustificationChanged}}
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(got), len(want), got)
	}
	for k, w := range want {
		if got[k].RequirementID != ids[w.i] || got[k].Kind != w.kind {
			t.Errorf("change %d = %s %s, want %s %s", k, got[k].Code, got[k].Kind, string(rune('A'+w.i)), w.kind)
		}
	}
	if got[0].Before != nil || got[0].After == nil {
		t.Errorf("added: before %v after %v", got[0].Before, got[0].After)
	}
	if got[1].Before == nil || got[1].After != nil {
		t.Errorf("removed: before %v after %v", got[1].Before, got[1].After)
	}
}

func TestDiffDecisions_NoBaselineEveryDecisionAdded(t *testing.T) {
	t.Parallel()
	target := []RequirementApplicability{
		{RequirementID: uuid.New(), Decision: &Decision{Decision: DecisionApplicable}},
		{RequirementID: uuid.New()},
	}
	got := DiffDecisions(nil, target)
	if len(got) != 1 || got[0].Kind != ChangeAdded {
		t.Fatalf("got %+v, want one added", got)
	}
	if empty := DiffDecisions(nil, nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty diff = %#v, want non-nil empty", empty)
	}
}

// A decision and justification change together is reported once, as a
// decision change.
func TestDiffDecisions_DecisionAndJustificationIsDecisionChange(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	base := []RequirementApplicability{{RequirementID: id, Decision: &Decision{Decision: DecisionExcluded, Justification: "x"}}}
	target := []RequirementApplicability{{RequirementID: id, Decision: &Decision{Decision: DecisionApplicable}}}
	got := DiffDecisions(base, target)
	if len(got) != 1 || got[0].Kind != ChangeDecisionChanged {
		t.Fatalf("got %+v", got)
	}
}
