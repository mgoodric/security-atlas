//go:build integration

// #2106 — integration tests for linking and unlinking a control from the
// risk page: POST /v1/risks/{id}/controls and DELETE
// /v1/risks/{id}/controls/{control_id}. Both write a risk_audit_log row,
// both refuse a closed risk, and unlinking the last control of a mitigate
// risk with no live action plan is refused. Real Postgres under RLS + the
// assembled router, reusing the #2105 env.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run ControlLink

package risks_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// newControlLinkEnv is the #2105 env plus cleanup of the controls, links
// and action plans these tests seed.
func newControlLinkEnv(t *testing.T) transitionEnv {
	t.Helper()
	env := newTransitionEnv(t)
	// Registered after the env's own cleanup, so it runs first: the link
	// rows go before the risks and controls they reference.
	t.Cleanup(func() {
		for _, table := range []string{"risk_control_links", "action_plan_risks", "action_plans", "controls"} {
			if _, err := env.admin.Exec(context.Background(),
				`DELETE FROM `+table+` WHERE tenant_id = $1`, env.tenant); err != nil {
				t.Errorf("cleanup %s: %v", table, err)
			}
		}
	})
	return env
}

func (env transitionEnv) link(t *testing.T, riskID string, controlID uuid.UUID, want int) map[string]any {
	t.Helper()
	code, body := env.do(t, http.MethodPost, "/v1/risks/"+riskID+"/controls", env.ownerJWT,
		map[string]any{"control_id": controlID.String()})
	if code != want {
		t.Fatalf("link %s: status %d, want %d: %v", controlID, code, want, body)
	}
	return body
}

func (env transitionEnv) unlink(t *testing.T, riskID string, controlID uuid.UUID, want int) map[string]any {
	t.Helper()
	code, body := env.do(t, http.MethodDelete, "/v1/risks/"+riskID+"/controls/"+controlID.String(), env.ownerJWT, nil)
	if code != want {
		t.Fatalf("unlink %s: status %d, want %d: %v", controlID, code, want, body)
	}
	return body
}

func linkedControlIDs(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, _ := riskField(t, body, "linked_control_ids").([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = v.(string)
	}
	slices.Sort(out)
	return out
}

// TestControlLink_AuditActionCheck: the CHECK keeps every earlier action
// and takes control_linked and control_unlinked.
func TestControlLink_AuditActionCheck(t *testing.T) {
	env := newControlLinkEnv(t)
	for _, action := range []string{
		"placement_changed", "category_changed", "fields_changed",
		"treatment_changed", "acceptance_proposed", "acceptance_approved",
		"acceptance_rejected", "acceptance_revoked",
		"closed", "reopened", "deleted",
		"control_linked", "control_unlinked",
	} {
		if _, err := env.admin.Exec(context.Background(), `
			INSERT INTO risk_audit_log (tenant_id, risk_id, actor, action_type, before_state, after_state)
			VALUES ($1, gen_random_uuid(), 'test', $2, '{}', '{}')`, env.tenant, action); err != nil {
			t.Fatalf("%s refused: %v", action, err)
		}
	}
}

// TestControlLink_LinkAndUnlinkAreLogged: linking two controls writes two
// control_linked rows; re-linking one writes nothing; unlinking one
// removes the link and writes one control_unlinked row naming it.
// Unlinking a control the risk does not link is a 404 and writes nothing.
func TestControlLink_LinkAndUnlinkAreLogged(t *testing.T) {
	env := newControlLinkEnv(t)
	id := env.newRisk(t)
	c1, c2 := seedControl(t, env.admin, env.tenant), seedControl(t, env.admin, env.tenant)

	env.link(t, id, c1, http.StatusOK)
	body := env.link(t, id, c2, http.StatusOK)
	want := []string{c1.String(), c2.String()}
	slices.Sort(want)
	if got := linkedControlIDs(t, body); !slices.Equal(got, want) {
		t.Fatalf("linked after two links = %v, want %v", got, want)
	}
	env.link(t, id, c1, http.StatusOK)

	body = env.unlink(t, id, c1, http.StatusOK)
	if got := linkedControlIDs(t, body); !slices.Equal(got, []string{c2.String()}) {
		t.Fatalf("linked after unlink = %v, want [%s]", got, c2)
	}
	var n int
	if err := env.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_control_links WHERE tenant_id = $1 AND risk_id = $2 AND control_id = $3`,
		env.tenant, id, c1).Scan(&n); err != nil || n != 0 {
		t.Fatalf("link row after unlink: count %d, err %v", n, err)
	}
	env.unlink(t, id, c1, http.StatusNotFound)
	env.unlink(t, id, uuid.New(), http.StatusNotFound)
	env.unlink(t, uuid.NewString(), c2, http.StatusNotFound)

	rows := env.auditRows(t, id)
	if got := actionsOf(rows); !slices.Equal(got, []string{"control_linked", "control_linked", "control_unlinked"}) {
		t.Fatalf("audit actions = %v, want [control_linked control_linked control_unlinked]", got)
	}
	unlinked := rows[2]
	if before, _ := unlinked["before"].(map[string]any); before["control_id"] != c1.String() {
		t.Errorf("control_unlinked before = %v, want control_id %s", unlinked["before"], c1)
	}
	if after, _ := unlinked["after"].(map[string]any); after["control_id"] != nil {
		t.Errorf("control_unlinked after = %v, want control_id null", unlinked["after"])
	}
	for _, r := range rows {
		if r["actor"] == "" {
			t.Errorf("%v row has no actor", r["action"])
		}
	}
}

// TestControlLink_LastControlOfMitigateRisk: a mitigate risk cannot lose
// its last control while it has no live action plan (400, nothing
// written). With a live plan linked, it can; a tombstoned plan does not
// count.
func TestControlLink_LastControlOfMitigateRisk(t *testing.T) {
	env := newControlLinkEnv(t)
	id := env.newRisk(t)
	c := seedControl(t, env.admin, env.tenant)
	env.link(t, id, c, http.StatusOK)
	env.step(t, id, "treatment", env.ownerJWT, map[string]any{"treatment": "mitigate"}, http.StatusOK)

	code, body := env.do(t, http.MethodDelete, "/v1/risks/"+id+"/controls/"+c.String(), env.ownerJWT, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("unlink last control of mitigate: status %d, want 400: %v", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "linked control or linked action plan") {
		t.Errorf("error = %q", msg)
	}

	plan := uuid.New()
	if _, err := env.admin.Exec(context.Background(), `
		INSERT INTO action_plans (id, tenant_id, title, owner_id, tombstoned_at)
		VALUES ($1, $2, 'Retired plan', $3, now())`, plan, env.tenant, env.owner); err != nil {
		t.Fatalf("seed tombstoned plan: %v", err)
	}
	if _, err := env.admin.Exec(context.Background(), `
		INSERT INTO action_plan_risks (action_plan_id, risk_id, tenant_id, linked_by)
		VALUES ($1, $2, $3, $4)`, plan, id, env.tenant, env.owner); err != nil {
		t.Fatalf("link tombstoned plan: %v", err)
	}
	env.unlink(t, id, c, http.StatusBadRequest)

	if _, err := env.admin.Exec(context.Background(),
		`UPDATE action_plans SET tombstoned_at = NULL WHERE id = $1`, plan); err != nil {
		t.Fatalf("revive plan: %v", err)
	}
	body = env.unlink(t, id, c, http.StatusOK)
	if got := linkedControlIDs(t, body); len(got) != 0 {
		t.Fatalf("linked after unlink = %v, want none", got)
	}
	if got := actionsOf(env.auditRows(t, id)); !slices.Equal(got, []string{"control_linked", "treatment_changed", "control_unlinked"}) {
		t.Fatalf("audit actions = %v", got)
	}
}

// TestControlLink_ClosedRiskRefusesLinkChanges: a closed risk takes no
// link or unlink (409) and writes no audit row for one.
func TestControlLink_ClosedRiskRefusesLinkChanges(t *testing.T) {
	env := newControlLinkEnv(t)
	id := env.newRisk(t)
	c1, c2 := seedControl(t, env.admin, env.tenant), seedControl(t, env.admin, env.tenant)
	env.link(t, id, c1, http.StatusOK)
	env.step(t, id, "close", env.ownerJWT, closeReason(), http.StatusOK)

	env.link(t, id, c2, http.StatusConflict)
	env.unlink(t, id, c1, http.StatusConflict)
	if got := actionsOf(env.auditRows(t, id)); !slices.Equal(got, []string{"control_linked", "closed"}) {
		t.Fatalf("audit actions = %v, want [control_linked closed]", got)
	}
}

// TestControlLink_OtherTenantsControlIsRefused: a control from another
// tenant is not found, linked or unlinked.
func TestControlLink_OtherTenantsControlIsRefused(t *testing.T) {
	env := newControlLinkEnv(t)
	other := newControlLinkEnv(t)
	id := env.newRisk(t)
	foreign := seedControl(t, other.admin, other.tenant)

	code, body := env.do(t, http.MethodPost, "/v1/risks/"+id+"/controls", env.ownerJWT,
		map[string]any{"control_id": foreign.String()})
	if code != http.StatusNotFound || body["error"] != "control not found" {
		t.Fatalf("link foreign control: status %d %v, want 404 control not found", code, body)
	}
	env.unlink(t, id, foreign, http.StatusNotFound)
	if got := actionsOf(env.auditRows(t, id)); len(got) != 0 {
		t.Fatalf("refused cross-tenant link wrote audit rows: %v", got)
	}
}

// TestControlLink_UnifiedLogNamesTheFieldOnly: the unified log's risk
// branch carries the link rows with changed_fields [control_id] and no
// control id.
func TestControlLink_UnifiedLogNamesTheFieldOnly(t *testing.T) {
	env := newControlLinkEnv(t)
	id := env.newRisk(t)
	c := seedControl(t, env.admin, env.tenant)
	env.link(t, id, c, http.StatusOK)
	env.unlink(t, id, c, http.StatusOK)

	now := time.Now().UTC()
	q := url.Values{
		"from": {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":   {now.Add(time.Hour).Format(time.RFC3339)},
		"kind": {"risk"},
	}
	code, body := env.do(t, http.MethodGet, "/v1/admin/audit-log/unified?"+q.Encode(), env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("unified audit log: status %d: %v", code, body)
	}
	seen := map[string]bool{}
	entries, _ := body["entries"].([]any)
	for _, raw := range entries {
		e := raw.(map[string]any)
		action, _ := e["action"].(string)
		if action != "control_linked" && action != "control_unlinked" {
			continue
		}
		seen[action] = true
		payload, _ := e["payload_json"].(map[string]any)
		if got := fmt.Sprint(payload["changed_fields"]); got != "[control_id]" {
			t.Errorf("%s changed_fields = %s, want [control_id]", action, got)
		}
		if strings.Contains(fmt.Sprint(e), c.String()) {
			t.Errorf("%s unified entry carries the control id: %v", action, e)
		}
	}
	if !seen["control_linked"] || !seen["control_unlinked"] {
		t.Fatalf("unified entries = %v, want control_linked and control_unlinked", entries)
	}
}
