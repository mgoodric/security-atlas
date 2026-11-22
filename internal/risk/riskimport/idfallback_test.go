package riskimport

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// TestParseCSV_IDColumns: the export's owner_user_id and org_unit_id are
// read as refs and also kept as unmapped values, since parsing cannot know
// whether they name anything in the uploading tenant (#2898).
func TestParseCSV_IDColumns(t *testing.T) {
	t.Parallel()
	owner, unit := uuid.NewString(), uuid.NewString()
	csv := "title,category,owner_user_id,org_unit_id\n" +
		"A,operational," + owner + "," + unit + "\n" +
		"B,operational,,\n"
	got, err := ParseCSVWith([]byte(csv), ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	if want := []string{"owner_user_id", "org_unit_id"}; !reflect.DeepEqual(got.UnmappedColumns, want) {
		t.Errorf("unmapped = %q, want %q", got.UnmappedColumns, want)
	}
	if want := map[string]string{FieldOwnerUserID: "owner_user_id", FieldOrgUnitID: "org_unit_id"}; !reflect.DeepEqual(got.idColumns, want) {
		t.Errorf("idColumns = %v, want %v", got.idColumns, want)
	}
	a, b := got.Risks[0], got.Risks[1]
	if a.OwnerUserIDRef != owner || a.OrgUnitIDRef != unit ||
		!reflect.DeepEqual(a.Unmapped, map[string]string{"owner_user_id": owner, "org_unit_id": unit}) {
		t.Errorf("row A = %+v", a)
	}
	if b.OwnerUserIDRef != "" || b.OrgUnitIDRef != "" || len(b.Unmapped) != 0 {
		t.Errorf("row B = %+v", b)
	}
}

func TestApplyPlacement_OrgUnitID(t *testing.T) {
	t.Parallel()
	payments := risk.OrgUnit{ID: uuid.New(), Name: "Payments", Level: dbx.RiskLevelTeam}
	platform := risk.OrgUnit{ID: uuid.New(), Name: "Platform", Level: dbx.RiskLevelOrg}
	byName := indexOrgUnits([]risk.OrgUnit{payments, platform})
	ref := payments.ID.String()
	byID := map[string]risk.OrgUnit{ref: payments}
	cases := []struct {
		name       string
		s          StagedRisk
		wantReason string
		wantOU     string
		wantValue  string
	}{
		{"resolved id", StagedRisk{OrgUnitIDRef: ref}, "", ref, "Payments"},
		{"resolved id at its unit's level", StagedRisk{OrgUnitIDRef: ref, Level: dbx.RiskLevelTeam}, "", ref, "Payments"},
		{"a name wins over the id", StagedRisk{OrgUnitIDRef: ref, OrgUnit: "Platform"}, "", platform.ID.String(), "Platform"},
		{"an unresolved id places nothing", StagedRisk{OrgUnitIDRef: uuid.NewString()}, "", "", ""},
		{"resolved id at another level", StagedRisk{OrgUnitIDRef: ref, Level: dbx.RiskLevelCompany}, RejectLevelMismatch, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var row BatchRow
			rej, ok := applyPlacement(&row, tc.s, byName, byID)
			if tc.wantReason != "" {
				if ok || rej.Reason != tc.wantReason {
					t.Fatalf("applyPlacement = %+v, %v; want %s", rej, ok, tc.wantReason)
				}
				if want := `level "company" differs from org unit "Payments"'s level "team"`; rej.Detail != want {
					t.Errorf("detail = %q, want %q", rej.Detail, want)
				}
				return
			}
			if !ok || row.OrgUnitID != tc.wantOU || row.OrgUnitValue != tc.wantValue {
				t.Errorf("applyPlacement = %+v, %v; org unit %q value %q", rej, ok, row.OrgUnitID, row.OrgUnitValue)
			}
		})
	}
}

func TestApplyOwner_LinkedByIDWins(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	row := BatchRow{OwnerUserID: id.String()}
	applyOwner(&row, "someone else", map[string]uuid.UUID{})
	if row.OwnerUserID != id.String() || row.OwnerUnlinked != "" || len(row.Flags) != 0 {
		t.Errorf("row = %+v", row)
	}
}

func TestDropUsedIDColumns(t *testing.T) {
	t.Parallel()
	b := Batch{
		UnmappedColumns: []string{"id", "owner_user_id", "org_unit_id"},
		Staged: []BatchRow{
			{Unmapped: map[string]string{"id": "x"}},
			{Unmapped: map[string]string{"org_unit_id": "kept"}},
		},
	}
	dropUsedIDColumns(&b, map[string]string{FieldOwnerUserID: "owner_user_id", FieldOrgUnitID: "org_unit_id"})
	if want := []string{"id", "org_unit_id"}; !reflect.DeepEqual(b.UnmappedColumns, want) {
		t.Errorf("unmapped = %q, want %q", b.UnmappedColumns, want)
	}
}
