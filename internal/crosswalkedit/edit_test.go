// Pure-Go unit coverage of the crosswalk content-edit validation layer
// (slice-353 Q-2 fast-loop convention): no Postgres, no build tag. Every
// pre-transaction branch of Resolve / ParseRelationshipType /
// ValidateStrength / TierEditable is pinned here; the same-tx audit-row
// discipline is proven against real Postgres by the
// internal/api/admincrosswalkreview integration suite.
package crosswalkedit

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func strPtr(s string) *string   { return &s }
func f64Ptr(f float64) *float64 { return &f }

func TestParseRelationshipType(t *testing.T) {
	t.Parallel()
	valid := []string{"equal", "subset_of", "superset_of", "intersects_with", "no_relationship"}
	for _, v := range valid {
		got, err := ParseRelationshipType(v)
		if err != nil {
			t.Errorf("ParseRelationshipType(%q): unexpected err %v", v, err)
		}
		if string(got) != v {
			t.Errorf("ParseRelationshipType(%q) = %q", v, got)
		}
	}
	for _, bad := range []string{"", "EQUAL", "equals", "related_to"} {
		if _, err := ParseRelationshipType(bad); !errors.Is(err, ErrUnknownRelationshipType) {
			t.Errorf("ParseRelationshipType(%q): want ErrUnknownRelationshipType, got %v", bad, err)
		}
	}
}

func TestValidateStrength(t *testing.T) {
	t.Parallel()
	for _, ok := range []float64{0, 0.5, 1} {
		if err := ValidateStrength(ok); err != nil {
			t.Errorf("ValidateStrength(%v): unexpected err %v", ok, err)
		}
	}
	for _, bad := range []float64{-0.01, 1.01, 2} {
		if err := ValidateStrength(bad); !errors.Is(err, ErrStrengthOutOfRange) {
			t.Errorf("ValidateStrength(%v): want ErrStrengthOutOfRange, got %v", bad, err)
		}
	}
}

// TestTierEditable pins the D-536b-1 gate: content is mutable at draft and
// under_review only — a verified mapping's content is exactly what was
// verified, and rejected is terminal.
func TestTierEditable(t *testing.T) {
	t.Parallel()
	if !TierEditable(crosswalktier.TierDraft) || !TierEditable(crosswalktier.TierUnderReview) {
		t.Error("draft and under_review must be editable")
	}
	if TierEditable(crosswalktier.TierVerified) {
		t.Error("verified must NOT be editable (demote via the tier state machine first)")
	}
	if TierEditable(crosswalktier.TierRejected) {
		t.Error("rejected must NOT be editable (terminal)")
	}
	if TierEditable(crosswalktier.Tier("bogus")) {
		t.Error("an unknown tier must NOT be editable (fail closed)")
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	current := Content{
		RelationshipType: dbx.StrmRelationshipTypeEqual,
		Strength:         1.0,
		Rationale:        "imported",
	}

	cases := []struct {
		name    string
		patch   ContentPatch
		want    Content
		wantErr error
	}{
		{
			name:    "empty patch is a malformed request",
			patch:   ContentPatch{},
			wantErr: ErrNoFields,
		},
		{
			name:  "single-field strength edit keeps the rest",
			patch: ContentPatch{Strength: f64Ptr(0.6)},
			want:  Content{RelationshipType: dbx.StrmRelationshipTypeEqual, Strength: 0.6, Rationale: "imported"},
		},
		{
			name: "full edit",
			patch: ContentPatch{
				RelationshipType: strPtr("subset_of"),
				Strength:         f64Ptr(0.7),
				Rationale:        strPtr("curated: partial coverage only"),
			},
			want: Content{RelationshipType: dbx.StrmRelationshipTypeSubsetOf, Strength: 0.7, Rationale: "curated: partial coverage only"},
		},
		{
			name:  "rationale may be cleared to empty",
			patch: ContentPatch{Rationale: strPtr("")},
			want:  Content{RelationshipType: dbx.StrmRelationshipTypeEqual, Strength: 1.0, Rationale: ""},
		},
		{
			name:    "unknown relationship type",
			patch:   ContentPatch{RelationshipType: strPtr("related_to")},
			wantErr: ErrUnknownRelationshipType,
		},
		{
			name:    "strength above 1 rejected",
			patch:   ContentPatch{Strength: f64Ptr(1.5)},
			wantErr: ErrStrengthOutOfRange,
		},
		{
			name:    "strength below 0 rejected",
			patch:   ContentPatch{Strength: f64Ptr(-0.1)},
			wantErr: ErrStrengthOutOfRange,
		},
		{
			name: "no-op patch resolving to identical content",
			patch: ContentPatch{
				RelationshipType: strPtr("equal"),
				Strength:         f64Ptr(1.0),
				Rationale:        strPtr("imported"),
			},
			wantErr: ErrNoChange,
		},
		{
			name:    "single-field no-op",
			patch:   ContentPatch{Strength: f64Ptr(1.0)},
			wantErr: ErrNoChange,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(current, tc.patch)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Resolve: want %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: unexpected err %v", err)
			}
			if got != tc.want {
				t.Fatalf("Resolve = %+v; want %+v", got, tc.want)
			}
		})
	}
}
