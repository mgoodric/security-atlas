package risks

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestDecodePatch covers the PATCH /v1/risks/{id} body rules (#1749): an
// omitted org_unit_id keeps the unit, an explicit null unbinds it, and any
// field other than level / org_unit_id is refused.
func TestDecodePatch(t *testing.T) {
	t.Parallel()
	ou := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := []struct {
		name       string
		body       string
		wantMsg    bool
		wantLevel  dbx.RiskLevel
		wantSet    bool
		wantUnitID *uuid.UUID
	}{
		{name: "level only", body: `{"level":"org"}`, wantLevel: dbx.RiskLevelOrg},
		{name: "unit only", body: `{"org_unit_id":"` + ou.String() + `"}`, wantSet: true, wantUnitID: &ou},
		{name: "explicit null unbinds", body: `{"org_unit_id":null}`, wantSet: true},
		{name: "both", body: `{"level":"team","org_unit_id":"` + ou.String() + `"}`, wantLevel: dbx.RiskLevelTeam, wantSet: true, wantUnitID: &ou},
		{name: "empty body", body: `{}`, wantMsg: true},
		{name: "empty level", body: `{"level":""}`, wantMsg: true},
		{name: "unknown field", body: `{"title":"x"}`, wantMsg: true},
		{name: "bad unit", body: `{"org_unit_id":"nope"}`, wantMsg: true},
		{name: "non-string unit", body: `{"org_unit_id":7}`, wantMsg: true},
		{name: "not json", body: `level=org`, wantMsg: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("PATCH", "/v1/risks/x", strings.NewReader(tc.body))
			in, msg := decodePatch(r)
			if tc.wantMsg {
				if msg == "" {
					t.Fatalf("want a 400 message, got none (in=%+v)", in)
				}
				return
			}
			if msg != "" {
				t.Fatalf("unexpected message: %s", msg)
			}
			if in.Level != tc.wantLevel || in.OrgUnitSet != tc.wantSet {
				t.Fatalf("in = %+v", in)
			}
			if (in.OrgUnitID == nil) != (tc.wantUnitID == nil) ||
				(in.OrgUnitID != nil && *in.OrgUnitID != *tc.wantUnitID) {
				t.Fatalf("org_unit_id = %v, want %v", in.OrgUnitID, tc.wantUnitID)
			}
		})
	}
}
