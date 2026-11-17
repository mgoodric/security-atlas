package risk

import (
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestTreatmentLifecycle_StatesAreEveryCombination: every treatment and
// acceptance status pairing the risks CHECKs admit (an acceptance status
// exactly when the treatment is accept) is a state of the table, and every
// table state is one of them. A treatment or acceptance status added to its
// enum fails here until the table draws it (#2147).
func TestTreatmentLifecycle_StatesAreEveryCombination(t *testing.T) {
	t.Parallel()
	if err := TreatmentLifecycle.Validate(); err != nil {
		t.Fatal(err)
	}
	var admitted []string
	for _, tr := range dbx.AllRiskTreatmentValues() {
		if tr != dbx.RiskTreatmentAccept {
			admitted = append(admitted, TreatmentState(tr, ""))
			continue
		}
		for _, a := range dbx.AllRiskAcceptanceStatusValues() {
			admitted = append(admitted, TreatmentState(tr, a))
		}
	}
	got := slices.Clone(TreatmentLifecycle.States)
	slices.Sort(got)
	slices.Sort(admitted)
	if !slices.Equal(got, admitted) {
		t.Fatalf("table states %v, enum combinations %v", got, admitted)
	}
	if s := TreatmentState(dbx.RiskTreatmentAvoid, dbx.RiskAcceptanceStatusProposed); slices.Contains(TreatmentLifecycle.States, s) {
		t.Fatalf("a non-accept treatment with an acceptance status mapped to table state %s", s)
	}
}

// TestStatusLifecycle_StatesAreEveryStatus: the open/closed table names
// every risk_status value.
func TestStatusLifecycle_StatesAreEveryStatus(t *testing.T) {
	t.Parallel()
	if err := StatusLifecycle.Validate(); err != nil {
		t.Fatal(err)
	}
	var enum []string
	for _, v := range dbx.AllRiskStatusValues() {
		enum = append(enum, string(v))
	}
	got := slices.Clone(StatusLifecycle.States)
	slices.Sort(got)
	slices.Sort(enum)
	if !slices.Equal(got, enum) {
		t.Fatalf("table states %v, risk_status %v", got, enum)
	}
}
