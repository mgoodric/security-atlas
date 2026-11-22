package riskimport

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestParseCSV_OwnerHeaders(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"Owner", "Risk Owner", "owner_email", "Owner Email", "treatment_owner"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCSVWith([]byte("Title,Category,"+header+"\nOutage,operational, Alice@Corp.test \n"), ParseOptions{})
			if err != nil {
				t.Fatalf("ParseCSVWith: %v", err)
			}
			if len(got.Risks) != 1 || got.Risks[0].Owner != "Alice@Corp.test" || len(got.UnmappedColumns) != 0 {
				t.Fatalf("risks = %+v, unmapped = %q", got.Risks, got.UnmappedColumns)
			}
		})
	}
	// A column map names the owner column when no alias does.
	got, err := ParseCSVWith([]byte("Title,Category,Accountable\nOutage,operational,bo@corp.test\n"),
		ParseOptions{ColumnMap: map[string]string{FieldOwner: "Accountable"}})
	if err != nil {
		t.Fatalf("ParseCSVWith with column map: %v", err)
	}
	if got.Risks[0].Owner != "bo@corp.test" {
		t.Errorf("mapped owner = %q", got.Risks[0].Owner)
	}
}

func TestApplyOwner(t *testing.T) {
	t.Parallel()
	alice := uuid.MustParse("6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2843")
	resolved := map[string]ownerMatch{"alice@corp.test": {ID: alice, Email: "Alice@corp.test"}}
	cases := []struct {
		name, cell, wantUser, wantEmail, wantUnlinked string
		wantFlags                                     []string
	}{
		{"matched, ignoring case and space", " ALICE@corp.test ", alice.String(), "Alice@corp.test", "", nil},
		{"unmatched email", "carol@corp.test", "", "", "carol@corp.test", []string{FlagOwnerUnlinked}},
		{"a name", "Platform team", "", "", "Platform team", []string{FlagOwnerUnlinked}},
		{"blank", "  ", "", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var row BatchRow
			applyOwner(&row, tc.cell, resolved)
			if row.OwnerUserID != tc.wantUser || row.OwnerEmail != tc.wantEmail || row.OwnerUnlinked != tc.wantUnlinked || !reflect.DeepEqual(row.Flags, tc.wantFlags) {
				t.Errorf("row = user %q unlinked %q flags %v", row.OwnerUserID, row.OwnerUnlinked, row.Flags)
			}
		})
	}
}
