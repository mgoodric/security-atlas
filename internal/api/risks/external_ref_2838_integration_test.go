//go:build integration

// #2838 — a risk carries the organisation's own risk ID (external_ref).
// POST /v1/risks sets it, PATCH changes or clears it (one fields_changed
// audit row per change), GET returns it, and the export carries it as a
// column in every format. It is unique within the tenant: a second risk
// with the same ID is a 409, on create and on PATCH. The same ID in two
// tenants is two risks.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run ExternalRef

package risks_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func freshRefTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "risk_audit_log", "risks")
}

func TestExternalRef_SetOnCreateChangedAndClearedByPatch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshRefTenant(t, admin)
	env := testServer(t, app, tenant)

	// Set on create, trimmed, and read back.
	resp, body := createPlaced(t, env, map[string]any{"external_ref": "  R-014 "})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, body)
	}
	id := riskField(t, body, "id").(string)
	if got := riskField(t, body, "external_ref"); got != "R-014" {
		t.Fatalf("create returned external_ref %v, want R-014", got)
	}
	if _, got := get(t, env, "/v1/risks/"+id); riskField(t, got, "external_ref") != "R-014" {
		t.Fatalf("GET reads back external_ref %v, want R-014", riskField(t, got, "external_ref"))
	}

	// A risk created without one reads null.
	resp, body = createPlaced(t, env, nil)
	if resp.StatusCode != http.StatusCreated || riskField(t, body, "external_ref") != nil {
		t.Fatalf("create without ref: status %d, external_ref %v", resp.StatusCode, riskField(t, body, "external_ref"))
	}

	// PATCH changes it and writes one fields_changed row with before/after.
	resp, body = patch(t, env, "/v1/risks/"+id, `{"external_ref":"R-015"}`)
	if resp.StatusCode != http.StatusOK || riskField(t, body, "external_ref") != "R-015" {
		t.Fatalf("patch: status %d, body %v", resp.StatusCode, body)
	}
	entries := auditEntries(t, env, id)
	last := entries[0]
	before, _ := last["before"].(map[string]any)
	after, _ := last["after"].(map[string]any)
	if last["action"] != "fields_changed" || before["external_ref"] != "R-014" || after["external_ref"] != "R-015" {
		t.Fatalf("audit row = %v", last)
	}

	// The same ID again changes nothing and writes no row.
	n := len(entries)
	if resp, body := patch(t, env, "/v1/risks/"+id, `{"external_ref":"R-015"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("same-ref patch status %d: %v", resp.StatusCode, body)
	}
	if got := len(auditEntries(t, env, id)); got != n {
		t.Fatalf("same-ref patch wrote %d audit rows", got-n)
	}

	// null clears it, and the clear is audited with a null after.
	resp, body = patch(t, env, "/v1/risks/"+id, `{"external_ref":null}`)
	if resp.StatusCode != http.StatusOK || riskField(t, body, "external_ref") != nil {
		t.Fatalf("clear: status %d, external_ref %v", resp.StatusCode, riskField(t, body, "external_ref"))
	}
	cleared, _ := auditEntries(t, env, id)[0]["after"].(map[string]any)
	if v, ok := cleared["external_ref"]; !ok || v != nil {
		t.Fatalf("clear audit after_state = %v", cleared)
	}

	// A non-string is a 400; so is one over 128 characters.
	if resp, body := patch(t, env, "/v1/risks/"+id, `{"external_ref":14}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("numeric ref: status %d: %v", resp.StatusCode, body)
	}
	if resp, body := createPlaced(t, env, map[string]any{"external_ref": strings.Repeat("x", 129)}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("long ref: status %d: %v", resp.StatusCode, body)
	}
}

func TestExternalRef_UniqueWithinTheTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshRefTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := createPlaced(t, env, map[string]any{"external_ref": "R-001"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create status %d: %v", resp.StatusCode, body)
	}

	// A second create with the same ID is refused with a clear error.
	resp, body = createPlaced(t, env, map[string]any{"external_ref": "R-001"})
	if resp.StatusCode != http.StatusConflict || body["code"] != "external_ref_taken" {
		t.Fatalf("duplicate create: status %d, body %v", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "external_ref is already used") {
		t.Fatalf("duplicate create error = %q", msg)
	}

	// PATCHing another risk onto the taken ID is refused, and leaves it.
	resp, body = createPlaced(t, env, map[string]any{"external_ref": "R-002"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("second create status %d: %v", resp.StatusCode, body)
	}
	second := riskField(t, body, "id").(string)
	resp, body = patch(t, env, "/v1/risks/"+second, `{"external_ref":"R-001"}`)
	if resp.StatusCode != http.StatusConflict || body["code"] != "external_ref_taken" {
		t.Fatalf("duplicate patch: status %d, body %v", resp.StatusCode, body)
	}
	if _, got := get(t, env, "/v1/risks/"+second); riskField(t, got, "external_ref") != "R-002" {
		t.Fatalf("refused patch changed external_ref to %v", riskField(t, got, "external_ref"))
	}

	// Any number of risks may carry none.
	for i := 0; i < 2; i++ {
		if resp, body := createPlaced(t, env, map[string]any{"external_ref": ""}); resp.StatusCode != http.StatusCreated {
			t.Fatalf("unset ref create %d: status %d: %v", i, resp.StatusCode, body)
		}
	}
}

func TestExternalRef_SameIDInTwoTenantsIsAllowed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshRefTenant(t, admin)
	tenantB := freshRefTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	respA, bodyA := createPlaced(t, envA, map[string]any{"external_ref": "R-014"})
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("tenant A create status %d: %v", respA.StatusCode, bodyA)
	}
	respB, bodyB := createPlaced(t, envB, map[string]any{"external_ref": "R-014"})
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("tenant B create with the same ref: status %d: %v", respB.StatusCode, bodyB)
	}
	idA := riskField(t, bodyA, "id").(string)

	// Tenant B cannot read tenant A's risk.
	if resp, _ := get(t, envB, "/v1/risks/"+idA); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("tenant B GET tenant A's risk: status %d, want 404", resp.StatusCode)
	}
}

func TestExternalRef_ExportedInEveryFormat(t *testing.T) {
	tenant := uuid.New()
	admin := dbtest.NewMigratePool(t)
	id := seedRiskForExport(t, tenant, "ref export risk", "operational")
	if _, err := admin.Exec(t.Context(), `UPDATE risks SET external_ref = 'R-2838' WHERE id = $1`, id); err != nil {
		t.Fatalf("set external_ref: %v", err)
	}
	other := uuid.New()
	otherID := seedRiskForExport(t, other, "other tenant risk", "operational")
	if _, err := admin.Exec(t.Context(), `UPDATE risks SET external_ref = 'R-OTHER-2838' WHERE id = $1`, otherID); err != nil {
		t.Fatalf("set other external_ref: %v", err)
	}

	for _, format := range []string{"csv", "json", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			r := newRiskExportRouter(t, tenant, true, []string{"owner"})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/risks/export?format="+format, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
			}
			text := extractRiskSearchableText(t, format, rec.Body.Bytes())
			if !strings.Contains(text, "external_ref") || !strings.Contains(text, "R-2838") {
				t.Errorf("%s export lacks the external_ref column or value", format)
			}
			if strings.Contains(text, "R-OTHER-2838") {
				t.Errorf("%s export carries another tenant's external_ref", format)
			}
		})
	}

	// In the CSV, the value sits under the external_ref header.
	r := newRiskExportRouter(t, tenant, true, []string{"owner"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/risks/export?format=csv", nil))
	rows := mustParseRiskCSV(t, rec.Body.String())
	col := -1
	for i, h := range rows[0] {
		if h == "external_ref" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("CSV header %v has no external_ref", rows[0])
	}
	for _, row := range rows[1:] {
		if row[0] == id.String() && row[col] != "R-2838" {
			t.Errorf("CSV external_ref = %q; want R-2838", row[col])
		}
	}
}
