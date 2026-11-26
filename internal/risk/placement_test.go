package risk

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestResolveLevel covers the #1749 rule: a risk bound to an org unit sits
// at that unit's level; an unbound risk may sit at any level.
func TestResolveLevel(t *testing.T) {
	t.Parallel()
	org := dbx.RiskLevelOrg
	cases := []struct {
		name      string
		requested dbx.RiskLevel
		unit      *dbx.RiskLevel
		fallback  dbx.RiskLevel
		want      dbx.RiskLevel
		wantErr   error
	}{
		{"no unit no level takes fallback", "", nil, dbx.RiskLevelTeam, dbx.RiskLevelTeam, nil},
		{"no unit with level", dbx.RiskLevelCompany, nil, dbx.RiskLevelTeam, dbx.RiskLevelCompany, nil},
		{"unit without level takes unit level", "", &org, dbx.RiskLevelTeam, dbx.RiskLevelOrg, nil},
		{"unit with matching level", dbx.RiskLevelOrg, &org, dbx.RiskLevelTeam, dbx.RiskLevelOrg, nil},
		{"unit with other level", dbx.RiskLevelCompany, &org, dbx.RiskLevelTeam, "", ErrLevelMismatch},
		{"unknown level", "division", nil, dbx.RiskLevelTeam, "", ErrInvalidRiskLevel},
		{"unknown level with unit", "division", &org, dbx.RiskLevelTeam, "", ErrInvalidRiskLevel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveLevel(tc.requested, tc.unit, tc.fallback)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if !IsPlacementValidation(err) {
					t.Fatalf("IsPlacementValidation(%v) = false", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("level = %q, want %q", got, tc.want)
			}
		})
	}
}
