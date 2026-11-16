//go:build integration

// #2104 — integration tests for editing a risk's record fields with
// PATCH /v1/risks/{id}, and the risk audit trail read through
// GET /v1/risks/{id}/audit-log. Real Postgres + the assembled router.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run Record

package risks_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func auditEntries(t *testing.T, env testEnv, riskID string) []map[string]any {
	t.Helper()
	resp, body := get(t, env, "/v1/risks/"+riskID+"/audit-log")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit-log status %d: %v", resp.StatusCode, body)
	}
	raw, _ := body["entries"].([]any)
	out := make([]map[string]any, len(raw))
	for i, e := range raw {
		out[i] = e.(map[string]any)
	}
	return out
}

// sameInstant reports whether a wire timestamp is the instant want,
// whatever zone the server rendered it in.
func sameInstant(v any, want string) bool {
	s, _ := v.(string)
	got, err := time.Parse(time.RFC3339Nano, s)
	w, _ := time.Parse(time.RFC3339, want)
	return err == nil && got.Equal(w)
}

func createRecordRisk(t *testing.T, env testEnv, extra map[string]any) string {
	t.Helper()
	resp, body := createPlaced(t, env, extra)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, body)
	}
	return riskField(t, body, "id").(string)
}

// TestRecord_AuditActionCheck: the widened CHECK keeps every earlier
// action, takes fields_changed, and still refuses anything else.
func TestRecord_AuditActionCheck(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshPlacementTenant(t, admin)
	insert := func(action string) error {
		_, err := admin.Exec(context.Background(), `
			INSERT INTO risk_audit_log (tenant_id, risk_id, actor, action_type, before_state, after_state)
			VALUES ($1, gen_random_uuid(), 'test', $2, '{}', '{}')`, tenant, action)
		return err
	}
	for _, action := range []string{"placement_changed", "category_changed", "fields_changed", "treatment_changed"} {
		if err := insert(action); err != nil {
			t.Fatalf("%s refused: %v", action, err)
		}
	}
	if err := insert("acceptance_expired"); err == nil {
		t.Fatal("an action outside the list was accepted")
	}
}

// TestRecord_PatchUpdatesEachField: every listed record field changes
// through PATCH and reads back.
func TestRecord_PatchUpdatesEachField(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	id := createRecordRisk(t, env, nil)

	cases := []struct {
		field string
		body  string
		check func(v any) bool
	}{
		{"title", `{"title":"Edited title"}`, func(v any) bool { return v == "Edited title" }},
		{"description", `{"description":"Edited description"}`, func(v any) bool { return v == "Edited description" }},
		{"category", `{"category":"privacy"}`, func(v any) bool { return v == "privacy" }},
		{"inherent_score", `{"inherent_score":{"likelihood":5,"impact":2}}`, func(v any) bool {
			m := v.(map[string]any)
			return m["likelihood"] == float64(5) && m["impact"] == float64(2)
		}},
		{"treatment_owner", `{"treatment_owner":"sec-lead"}`, func(v any) bool { return v == "sec-lead" }},
		{"review_due_at", `{"review_due_at":"2027-04-01T00:00:00Z"}`, func(v any) bool {
			return sameInstant(v, "2027-04-01T00:00:00Z")
		}},
		{"instrument_reference", `{"instrument_reference":"POL-42"}`, func(v any) bool { return v == "POL-42" }},
		{"methodology", `{"methodology":"fair","inherent_score":{"loss_event_frequency":2,"loss_magnitude":1000}}`, func(v any) bool { return v == "fair" }},
	}
	for _, tc := range cases {
		resp, body := patch(t, env, "/v1/risks/"+id, tc.body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %v", tc.field, resp.StatusCode, body)
		}
		if v := riskField(t, body, tc.field); !tc.check(v) {
			t.Fatalf("%s: PATCH response has %v", tc.field, v)
		}
		_, got := get(t, env, "/v1/risks/"+id)
		if v := riskField(t, got, tc.field); !tc.check(v) {
			t.Fatalf("%s: GET reads back %v", tc.field, v)
		}
	}
	// The FAIR residual was reset onto the new scale.
	_, got := get(t, env, "/v1/risks/"+id)
	if res, _ := riskField(t, got, "residual_score").(map[string]any); res["annualized_loss_exposure"] != float64(2000) {
		t.Fatalf("residual_score after methodology change = %v", res)
	}
}

// TestRecord_PatchRefusesTreatmentAndAcceptance: treatment and acceptance
// fields are a 400 naming the field, and nothing changes.
func TestRecord_PatchRefusesTreatmentAndAcceptance(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	id := createRecordRisk(t, env, nil)

	for _, body := range []string{
		`{"title":"x","treatment":"avoid"}`,
		`{"acceptance_status":"approved"}`,
		`{"accepted_until":"2027-01-01"}`,
		`{"accepter":"ciso"}`,
	} {
		resp, got := patch(t, env, "/v1/risks/"+id, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %v", body, resp.StatusCode, got)
		}
		if msg, _ := got["error"].(string); !strings.Contains(msg, "cannot be changed by PATCH") {
			t.Fatalf("%s: error %q does not say why", body, msg)
		}
	}
	for _, body := range []string{`{"title":"  "}`, `{"inherent_score":{"lef":1}}`, `{"methodology":"nope"}`} {
		if resp, got := patch(t, env, "/v1/risks/"+id, body); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %v", body, resp.StatusCode, got)
		}
	}
	_, got := get(t, env, "/v1/risks/"+id)
	if riskField(t, got, "title") != "placement risk" || riskField(t, got, "treatment") != "untreated" {
		t.Fatalf("refused PATCH changed the risk: %v", got)
	}
	if n := len(auditEntries(t, env, id)); n != 0 {
		t.Fatalf("refused PATCHes wrote %d audit rows", n)
	}
}

// TestRecord_AuditTrail: each change writes one entry with before and
// after, a no-op writes none, and the read route returns them newest
// first.
func TestRecord_AuditTrail(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	id := createRecordRisk(t, env, nil)

	steps := []string{
		`{"title":"Second title"}`,
		`{"review_due_at":"2027-05-01T00:00:00Z","treatment_owner":"owner-b"}`,
		`{"category":"privacy"}`,
		`{"level":"org"}`,
		`{"title":"Second title"}`, // no-op: no row
	}
	for _, body := range steps {
		if resp, got := patch(t, env, "/v1/risks/"+id, body); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %v", body, resp.StatusCode, got)
		}
	}

	entries := auditEntries(t, env, id)
	wantActions := []string{"placement_changed", "category_changed", "fields_changed", "fields_changed"}
	if len(entries) != len(wantActions) {
		t.Fatalf("got %d entries, want %d: %v", len(entries), len(wantActions), entries)
	}
	for i, e := range entries {
		if e["action"] != wantActions[i] {
			t.Fatalf("entry %d action %v, want %s (newest first)", i, e["action"], wantActions[i])
		}
		if e["actor"] == "" || e["created_at"] == "" {
			t.Fatalf("entry %d missing actor or time: %v", i, e)
		}
		if i > 0 && e["created_at"].(string) > entries[i-1]["created_at"].(string) {
			t.Fatalf("entries out of order: %v", entries)
		}
	}

	title := entries[3]
	before, after := title["before"].(map[string]any), title["after"].(map[string]any)
	if before["title"] != "placement risk" || after["title"] != "Second title" || len(after) != 1 {
		t.Fatalf("title entry before=%v after=%v", before, after)
	}
	two := entries[2]["after"].(map[string]any)
	if two["treatment_owner"] != "owner-b" || !sameInstant(two["review_due_at"], "2027-05-01T00:00:00Z") || len(two) != 2 {
		t.Fatalf("second entry after=%v", two)
	}
	if entries[2]["before"].(map[string]any)["review_due_at"] == nil {
		t.Fatal("second entry has no before review_due_at")
	}

	// Another tenant reads no history for this risk.
	other := testServer(t, app, freshPlacementTenant(t, admin))
	if n := len(auditEntries(t, other, id)); n != 0 {
		t.Fatalf("cross-tenant read saw %d entries", n)
	}
}
