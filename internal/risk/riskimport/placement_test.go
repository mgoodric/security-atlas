package riskimport

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

func TestParseCSV_Placement(t *testing.T) {
	t.Parallel()
	csv := "Title,Category,Risk Level,Org Unit\n" +
		"A,operational,Team,Payments\n" +
		"B,operational, COMPANY ,\n" +
		"C,operational,,Platform\n" +
		"D,operational,division,Payments\n"
	got, err := ParseCSVWith([]byte(csv), ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	if len(got.UnmappedColumns) != 0 {
		t.Errorf("unmapped = %q", got.UnmappedColumns)
	}
	want := []struct {
		level   dbx.RiskLevel
		orgUnit string
	}{{dbx.RiskLevelTeam, "Payments"}, {dbx.RiskLevelCompany, ""}, {"", "Platform"}}
	if len(got.Risks) != len(want) {
		t.Fatalf("risks = %+v", got.Risks)
	}
	for i, w := range want {
		if r := got.Risks[i]; r.Level != w.level || r.OrgUnit != w.orgUnit {
			t.Errorf("row %d: level %q org unit %q; want %q %q", r.RowNumber, r.Level, r.OrgUnit, w.level, w.orgUnit)
		}
	}
	if len(got.Rejected) != 1 || got.Rejected[0].RowNumber != 5 || got.Rejected[0].Reason != RejectInvalidLevel {
		t.Errorf("rejected = %+v", got.Rejected)
	}

	// A column map names the placement columns when no alias does, and the
	// export's org_unit_id (a uuid) is never read as an org unit name.
	got, err = ParseCSVWith([]byte("Title,Category,Tier,Owning group,org_unit_id\nA,operational,org,Payments,"+uuid.NewString()+"\n"),
		ParseOptions{ColumnMap: map[string]string{FieldLevel: "Tier", FieldOrgUnit: "Owning group"}})
	if err != nil {
		t.Fatalf("ParseCSVWith with column map: %v", err)
	}
	if r := got.Risks[0]; r.Level != dbx.RiskLevelOrg || r.OrgUnit != "Payments" || r.Unmapped["org_unit_id"] == "" {
		t.Errorf("mapped row = %+v", r)
	}
}

func TestApplyPlacement(t *testing.T) {
	t.Parallel()
	payments := risk.OrgUnit{ID: uuid.MustParse("0b5e7c1e-2845-4a10-9c0e-6d1f2a3b4c5d"), Name: "Payments", Level: dbx.RiskLevelTeam}
	platform := risk.OrgUnit{ID: uuid.New(), Name: "Platform", Level: dbx.RiskLevelOrg}
	units := indexOrgUnits([]risk.OrgUnit{
		payments, platform,
		{ID: uuid.New(), Name: "Shared", Level: dbx.RiskLevelTeam},
		{ID: uuid.New(), Name: "shared ", Level: dbx.RiskLevelOrg},
	})
	cases := []struct {
		name              string
		level             dbx.RiskLevel
		orgUnit           string
		wantReason        string
		wantLevel, wantOU string
	}{
		{"neither", "", "", "", "", ""},
		{"level only", dbx.RiskLevelCompany, "", "", "company", ""},
		{"unit only, ignoring case and space", "", "  payments ", "", "", payments.ID.String()},
		{"matching level and unit", dbx.RiskLevelOrg, "Platform", "", "org", platform.ID.String()},
		{"unknown unit", "", "Treasury", RejectUnknownOrgUnit, "", ""},
		{"shared name", "", "SHARED", RejectAmbiguousOrgUnit, "", ""},
		{"level differs from unit", dbx.RiskLevelCompany, "Payments", RejectLevelMismatch, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var row BatchRow
			rej, ok := applyPlacement(&row, StagedRisk{RowNumber: 2, ExternalRef: "R-1", Level: tc.level, OrgUnit: tc.orgUnit}, units)
			if tc.wantReason != "" {
				if ok || rej.Reason != tc.wantReason || rej.RowNumber != 2 || rej.ExternalRef != "R-1" || rej.Detail == "" {
					t.Fatalf("applyPlacement = %+v, %v; want rejection %s", rej, ok, tc.wantReason)
				}
				return
			}
			if !ok || row.Level != tc.wantLevel || row.OrgUnitID != tc.wantOU {
				t.Errorf("applyPlacement = %+v, %v; row level %q org unit %q", rej, ok, row.Level, row.OrgUnitID)
			}
		})
	}
}
