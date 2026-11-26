package risk

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestCreatePathsRequireAnActor covers #2835: no create path and no theme
// write runs without an actor to name on its audit row. Each is refused
// before the store reaches the database (the store has no pool).
func TestCreatePathsRequireAnActor(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateInput{Title: "t", CategoryRef: "operational"}); !IsTransitionInput(err) {
		t.Errorf("Create without actor: err = %v, want transition input error", err)
	}
	if _, err := s.Aggregate(ctx, AggregateInput{
		ParentTitle:      "p",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{uuid.New()},
	}); !IsTransitionInput(err) {
		t.Errorf("Aggregate without actor: err = %v, want transition input error", err)
	}
	if _, err := s.AssignThemes(ctx, uuid.New(), []string{"ownership"}, ""); !IsTransitionInput(err) {
		t.Errorf("AssignThemes without actor: err = %v, want transition input error", err)
	}
}

// TestCreatedStateAfter pins the created row's after state: the six
// initial fields by wire name, an unset owner or risk ID as null (#2838).
func TestCreatedStateAfter(t *testing.T) {
	t.Parallel()
	cat, owner := uuid.New(), uuid.New()
	got := CreatedState{Title: "t", CategoryID: cat, Treatment: dbx.RiskTreatmentUntreated, TreatmentOwner: "ops"}.after()
	want := map[string]any{
		"title":           "t",
		"category_id":     cat,
		"treatment":       dbx.RiskTreatmentUntreated,
		"owner_user_id":   nil,
		"treatment_owner": "ops",
		"external_ref":    nil,
	}
	if len(got) != len(want) {
		t.Fatalf("after = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("after[%s] = %v, want %v", k, got[k], v)
		}
	}
	if a := (CreatedState{OwnerUserID: &owner}).after(); a["owner_user_id"] != owner.String() {
		t.Errorf("owner_user_id = %v, want %s", a["owner_user_id"], owner)
	}
	ref := "R-014"
	if a := (CreatedState{ExternalRef: &ref}).after(); a["external_ref"] != "R-014" {
		t.Errorf("external_ref = %v, want R-014", a["external_ref"])
	}
}
