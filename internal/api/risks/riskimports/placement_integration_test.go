//go:build integration

// #2845 — imported risks are placed in the organisation's hierarchy: a
// level column maps to the risk's level, and an org unit column is matched
// by name to the uploading tenant's org units, under the create path's
// placement rule. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// seedOrgUnit inserts an org unit of tenant with the given name and level.
func seedOrgUnit(t *testing.T, admin *pgxpool.Pool, tenant, name, level string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO org_units (id, tenant_id, name, level) VALUES ($1, $2, $3, $4::risk_level)`,
		id, tenant, name, level); err != nil {
		t.Fatalf("seed org unit %s: %v", name, err)
	}
	return id
}

// riskPlacement reads a created risk's level and org unit ("" when none).
func riskPlacement(t *testing.T, e env, id string) (string, string) {
	t.Helper()
	var level, unit string
	if err := e.admin.QueryRow(context.Background(), `
		SELECT level::text, COALESCE(org_unit_id::text, '') FROM risks WHERE id = $1`, id).Scan(&level, &unit); err != nil {
		t.Fatalf("read placement of risk %s: %v", id, err)
	}
	return level, unit
}

func TestRiskImport_Placement(t *testing.T) {
	e := newEnv(t)
	other := dbtest.SeedTenant(t, e.admin, cleanupTables()...)
	payments := seedOrgUnit(t, e.admin, e.tenant, "Payments", "team")
	platform := seedOrgUnit(t, e.admin, e.tenant, "Platform", "org")
	group := seedOrgUnit(t, e.admin, e.tenant, "Group", "company")
	ops := seedOrgUnit(t, e.admin, e.tenant, "Ops", "team")
	seedOrgUnit(t, e.admin, e.tenant, "Shared", "team")
	seedOrgUnit(t, e.admin, e.tenant, "shared", "org")
	// Another tenant's units are never matched: a name only it holds is
	// unknown here, and a name both hold matches this tenant's unit.
	foreignPayments := seedOrgUnit(t, e.admin, other, "Payments", "team")
	seedOrgUnit(t, e.admin, other, "Treasury", "team")

	csv := "Risk ID,Title,Category,Level,Org Unit\n" +
		"R-TEAM,Team risk,operational,team,\n" +
		"R-ORG,Org risk,operational,Org,\n" +
		"R-COMPANY,Company risk,operational,COMPANY,\n" +
		"R-NONE,Unplaced risk,operational,,\n" +
		"R-PAY,Unit names the level,operational,, payments \n" +
		"R-PLAT,Level and unit agree,operational,org,Platform\n" +
		"R-GROUP,Company unit,operational,company,GROUP\n" +
		"R-OPS,Unit moved after upload,operational,team,Ops\n" +
		"R-TREASURY,Another tenant's unit,operational,,Treasury\n" +
		"R-NOWHERE,Unit nobody has,operational,,Nowhere\n" +
		"R-SHARED,Unit name two units share,operational,,Shared\n" +
		"R-MISMATCH,Level differs from unit,operational,company,Payments\n" +
		"R-BADLEVEL,Level that is no level,operational,division,\n"

	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	if b.StagedCount != 8 || b.RejectedCount != 5 || b.TotalRows != 13 || len(b.UnmappedColumns) != 0 {
		t.Fatalf("batch staged=%d rejected=%+v total=%d unmapped=%q", b.StagedCount, b.Rejected, b.TotalRows, b.UnmappedColumns)
	}

	rejected := map[string]string{
		"R-TREASURY": riskimport.RejectUnknownOrgUnit,
		"R-NOWHERE":  riskimport.RejectUnknownOrgUnit,
		"R-SHARED":   riskimport.RejectAmbiguousOrgUnit,
		"R-MISMATCH": riskimport.RejectLevelMismatch,
		"R-BADLEVEL": riskimport.RejectInvalidLevel,
	}
	for _, r := range b.Rejected {
		if want, ok := rejected[r.ExternalRef]; !ok || r.Reason != want || r.Detail == "" {
			t.Errorf("rejected %s = %s %q; want %s", r.ExternalRef, r.Reason, r.Detail, want)
		}
	}
	if !strings.Contains(rejectedDetail(b, "R-TREASURY"), `"Treasury"`) {
		t.Errorf("unknown org unit detail = %q", rejectedDetail(b, "R-TREASURY"))
	}

	type placement struct{ level, unit string }
	staged := map[string]placement{
		"R-TEAM":    {"team", ""},
		"R-ORG":     {"org", ""},
		"R-COMPANY": {"company", ""},
		"R-NONE":    {"", ""},
		"R-PAY":     {"", payments.String()},
		"R-PLAT":    {"org", platform.String()},
		"R-GROUP":   {"company", group.String()},
		"R-OPS":     {"team", ops.String()},
	}
	// The upload response and a later read of the batch agree.
	status, raw = call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get batch = %d %s", status, raw)
	}
	for _, got := range []riskimport.Batch{b, decode[riskimport.Batch](t, raw)} {
		for ref, w := range staged {
			r := stagedByRef(t, got, ref)
			if r.Level != w.level || r.OrgUnitID != w.unit || (w.unit == "") != (r.OrgUnitValue == "") {
				t.Errorf("%s staged level=%q org unit=%q (%q); want %+v", ref, r.Level, r.OrgUnitID, r.OrgUnitValue, w)
			}
			if r.OrgUnitID == foreignPayments.String() {
				t.Errorf("%s matched another tenant's org unit", ref)
			}
		}
	}

	// Ops moves to the org level between upload and approval. Approval
	// creates through the risk store, whose placement rule now refuses a
	// team risk in an org unit: the row is skipped and stays pending.
	if _, err := e.admin.Exec(context.Background(), `UPDATE org_units SET level = 'org' WHERE id = $1`, ops); err != nil {
		t.Fatalf("move ops: %v", err)
	}
	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	res := decode[riskimport.ApproveResult](t, raw)
	if res.ApprovedCount != 7 || len(res.Skipped) != 1 || res.Skipped[0].ExternalRef != "R-OPS" ||
		!strings.Contains(res.Skipped[0].Reason, "level must match") {
		t.Fatalf("approve result = %+v", res)
	}
	if r := stagedByRef(t, getBatch(t, e, b.ID), "R-OPS"); r.Status != riskimport.RowStatusPending {
		t.Errorf("R-OPS status = %s, want pending", r.Status)
	}

	created := map[string]placement{
		"R-TEAM":    {"team", ""},
		"R-ORG":     {"org", ""},
		"R-COMPANY": {"company", ""},
		// Neither a level nor a unit: a team risk, as a create makes it.
		"R-NONE": {"team", ""},
		// A unit and no level: the unit's level.
		"R-PAY":   {"team", payments.String()},
		"R-PLAT":  {"org", platform.String()},
		"R-GROUP": {"company", group.String()},
	}
	for _, d := range res.Approved {
		w, ok := created[d.ExternalRef]
		if !ok {
			t.Errorf("unexpected approved row %q", d.ExternalRef)
			continue
		}
		if level, unit := riskPlacement(t, e, d.RiskID); level != w.level || unit != w.unit {
			t.Errorf("%s risk level=%q org unit=%q; want %+v", d.ExternalRef, level, unit, w)
		}
	}

	// The other tenant's upload of the same file matches its own Payments
	// and finds none of this tenant's units.
	oe := newEnvFor(t, e.admin, e.app, other)
	status, raw = upload(t, oe, oe.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("other tenant upload = %d %s", status, raw)
	}
	ob := decode[riskimport.Batch](t, raw)
	if r := stagedByRef(t, ob, "R-PAY"); r.OrgUnitID != foreignPayments.String() {
		t.Errorf("other tenant R-PAY org unit = %q, want its own %s", r.OrgUnitID, foreignPayments)
	}
	if r := stagedByRef(t, ob, "R-TREASURY"); r.OrgUnitID == "" {
		t.Errorf("other tenant R-TREASURY staged as %+v, want its own Treasury", r)
	}
	for _, ref := range []string{"R-PLAT", "R-GROUP", "R-OPS", "R-SHARED"} {
		if got := rejectedReason(ob, ref); got != riskimport.RejectUnknownOrgUnit {
			t.Errorf("other tenant %s rejected as %q, want %s", ref, got, riskimport.RejectUnknownOrgUnit)
		}
	}
}

func getBatch(t *testing.T, e env, id string) riskimport.Batch {
	t.Helper()
	status, raw := call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("get batch = %d %s", status, raw)
	}
	return decode[riskimport.Batch](t, raw)
}

func rejectedReason(b riskimport.Batch, ref string) string {
	for _, r := range b.Rejected {
		if r.ExternalRef == ref {
			return r.Reason
		}
	}
	return ""
}

func rejectedDetail(b riskimport.Batch, ref string) string {
	for _, r := range b.Rejected {
		if r.ExternalRef == ref {
			return r.Detail
		}
	}
	return ""
}
