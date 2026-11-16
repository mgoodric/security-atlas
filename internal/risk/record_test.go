package risk

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func strp(s string) *string { return &s }

// TestApplyRecordFields covers #2104's record-field rules: only changed
// fields land in the diff, a no-op is an empty diff, and the refusals.
func TestApplyRecordFields(t *testing.T) {
	t.Parallel()
	due := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	base := Risk{
		Title:         "Old",
		Description:   "desc",
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":3,"impact":3}`),
		ResidualScore: []byte(`{"score":4}`),
		Treatment:     dbx.RiskTreatmentUntreated,
		ReviewDueAt:   &due,
	}

	t.Run("changed fields only", func(t *testing.T) {
		t.Parallel()
		later := due.AddDate(0, 6, 0)
		next, before, after, err := applyRecordFields(base, RecordFields{
			Title:       strp("New"),
			Description: strp("desc"),
			ReviewDueAt: &later,
		})
		if err != nil {
			t.Fatal(err)
		}
		if next.Title != "New" || !next.ReviewDueAt.Equal(later) {
			t.Fatalf("next = %+v", next)
		}
		if len(after) != 2 || after["title"] != "New" || before["title"] != "Old" {
			t.Fatalf("before=%v after=%v", before, after)
		}
		if _, ok := after["description"]; ok {
			t.Fatal("unchanged description in the diff")
		}
	})

	t.Run("no-op", func(t *testing.T) {
		t.Parallel()
		_, _, after, err := applyRecordFields(base, RecordFields{
			Title:         strp("Old"),
			InherentScore: json.RawMessage(`{"impact":3, "likelihood":3}`),
		})
		if err != nil || len(after) != 0 {
			t.Fatalf("after=%v err=%v", after, err)
		}
	})

	t.Run("methodology change resets residual", func(t *testing.T) {
		t.Parallel()
		fair := dbx.RiskMethodologyFair
		next, before, after, err := applyRecordFields(base, RecordFields{
			Methodology:   &fair,
			InherentScore: json.RawMessage(`{"lef":2,"lm":100}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !sameJSON(next.ResidualScore, next.InherentScore) {
			t.Fatalf("residual = %s, want the FAIR inherent", next.ResidualScore)
		}
		if after["methodology"] != "fair" || before["methodology"] != "nist_800_30" {
			t.Fatalf("before=%v after=%v", before, after)
		}
		if _, ok := after["residual_score"]; !ok {
			t.Fatal("residual reset not in the diff")
		}
	})

	refusals := []struct {
		name string
		risk Risk
		in   RecordFields
		want error
	}{
		{"empty title", base, RecordFields{Title: strp("  ")}, ErrRecordFieldInvalid},
		{"score off methodology", base, RecordFields{InherentScore: json.RawMessage(`{"lef":1}`)}, ErrInherentScoreInvalid},
		{"unknown methodology", base, RecordFields{Methodology: func() *dbx.RiskMethodology { m := dbx.RiskMethodology("x"); return &m }()}, ErrInvalidMethodology},
		{"transfer loses instrument", func() Risk {
			r := base
			r.Treatment = dbx.RiskTreatmentTransfer
			r.InstrumentReference = "P-1"
			return r
		}(),
			RecordFields{InstrumentReference: strp("")}, ErrRecordFieldInvalid},
		{"rule-generated score", func() Risk {
			r := base
			r.InherentScore = []byte(`{"likelihood":3,"impact":3,"rule_generated":true}`)
			return r
		}(), RecordFields{InherentScore: json.RawMessage(`{"likelihood":1,"impact":1}`)}, ErrRuleGeneratedScore},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := applyRecordFields(tc.risk, tc.in)
			if !errors.Is(err, tc.want) || !IsRecordValidation(err) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
