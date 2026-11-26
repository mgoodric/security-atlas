//go:build integration

// #2107 — integration tests for the risk open/closed lifecycle: PATCH
// /v1/risks/{id}/close and /reopen, closed risks left out of the register
// list, its count, the heatmap and the board brief's top risks, and hard
// delete reserved for admins with a trail that outlives the risk. Real
// Postgres under RLS + the assembled router, reusing the #2105 env.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run Lifecycle

package risks_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func closeReason() map[string]any {
	return map[string]any{"reason": "not_applicable", "note": "Lease ended; site decommissioned."}
}

// listIDs reads GET /v1/risks with query and returns the ids and the count.
func (env transitionEnv) listIDs(t *testing.T, query string) ([]string, int) {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/v1/risks"+query, env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("list %s: status %d: %v", query, code, body)
	}
	raw, _ := body["risks"].([]any)
	ids := make([]string, len(raw))
	for i, r := range raw {
		ids[i] = r.(map[string]any)["id"].(string)
	}
	return ids, int(body["count"].(float64))
}

// TestLifecycle_AuditActionCheck: the CHECK keeps every earlier action,
// takes closed, reopened and deleted, and refuses anything else.
func TestLifecycle_AuditActionCheck(t *testing.T) {
	env := newTransitionEnv(t)
	insert := func(action string) error {
		_, err := env.admin.Exec(context.Background(), `
			INSERT INTO risk_audit_log (tenant_id, risk_id, actor, action_type, before_state, after_state)
			VALUES ($1, gen_random_uuid(), 'test', $2, '{}', '{}')`, env.tenant, action)
		return err
	}
	for _, action := range []string{
		"placement_changed", "category_changed", "fields_changed",
		"treatment_changed", "acceptance_proposed", "acceptance_approved",
		"acceptance_rejected", "acceptance_revoked",
		"closed", "reopened", "deleted",
	} {
		if err := insert(action); err != nil {
			t.Fatalf("%s refused: %v", action, err)
		}
	}
	if err := insert("archived"); err == nil {
		t.Fatal("an action outside the list was accepted")
	}
}

// TestLifecycle_ClosureConsistencyCheck: the database refuses a closed
// risk with no reason and an open risk that carries one.
func TestLifecycle_ClosureConsistencyCheck(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	for _, stmt := range []string{
		`UPDATE risks SET status = 'closed' WHERE id = $1`,
		`UPDATE risks SET closure_reason = 'merged' WHERE id = $1`,
	} {
		if _, err := env.admin.Exec(context.Background(), stmt, id); err == nil {
			t.Errorf("%s: accepted, want the CHECK to refuse it", stmt)
		}
	}
}

// TestLifecycle_CloseRequiresAValidReason: close without a reason, or with
// one outside the list, is a 400 and changes nothing.
func TestLifecycle_CloseRequiresAValidReason(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	env.step(t, id, "close", env.ownerJWT, map[string]any{}, http.StatusBadRequest)
	env.step(t, id, "close", env.ownerJWT, map[string]any{"note": "no reason"}, http.StatusBadRequest)
	env.step(t, id, "close", env.ownerJWT, map[string]any{"reason": "duplicate"}, http.StatusBadRequest)
	env.step(t, id, "close", env.ownerJWT, map[string]any{"reason": "merged", "bogus": 1}, http.StatusBadRequest)
	env.step(t, uuid.NewString(), "close", env.ownerJWT, closeReason(), http.StatusNotFound)
	if got := actionsOf(env.auditRows(t, id)); len(got) != 0 {
		t.Fatalf("refused closes wrote audit rows: %v", got)
	}
	code, body := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if code != http.StatusOK || riskField(t, body, "status") != "open" {
		t.Fatalf("risk after refused closes: status %d, %v", code, body)
	}
}

// TestLifecycle_CloseThenReopenIsLogged: close records the reason, note,
// closer and time; reopen clears them. Each is one audit row, the reopen
// row keeping the closure it cleared. Closing a closed risk or reopening
// an open one is a 409.
func TestLifecycle_CloseThenReopenIsLogged(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)

	env.step(t, id, "reopen", env.ownerJWT, nil, http.StatusConflict)

	body := env.step(t, id, "close", env.ownerJWT, closeReason(), http.StatusOK)
	if got := riskField(t, body, "status"); got != "closed" {
		t.Fatalf("status after close = %v, want closed", got)
	}
	closure, _ := riskField(t, body, "closure").(map[string]any)
	if closure["reason"] != "not_applicable" || closure["note"] != "Lease ended; site decommissioned." {
		t.Fatalf("closure = %v", closure)
	}
	if closure["closed_by"] == "" || closure["closed_at"] == nil {
		t.Fatalf("closure lacks closer or time: %v", closure)
	}

	env.step(t, id, "close", env.ownerJWT, map[string]any{"reason": "merged"}, http.StatusConflict)

	body = env.step(t, id, "reopen", env.ownerJWT, nil, http.StatusOK)
	if got := riskField(t, body, "status"); got != "open" {
		t.Fatalf("status after reopen = %v, want open", got)
	}
	if got := riskField(t, body, "closure"); got != nil {
		t.Fatalf("closure after reopen = %v, want absent", got)
	}

	rows := env.auditRows(t, id)
	if got := actionsOf(rows); !slices.Equal(got, []string{"closed", "reopened"}) {
		t.Fatalf("audit actions = %v, want [closed reopened]", got)
	}
	closedAfter, _ := rows[0]["after"].(map[string]any)
	if closedAfter["status"] != "closed" || closedAfter["closure_reason"] != "not_applicable" ||
		closedAfter["closure_note"] != "Lease ended; site decommissioned." {
		t.Errorf("closed row after = %v", rows[0]["after"])
	}
	reopenBefore, _ := rows[1]["before"].(map[string]any)
	if reopenBefore["status"] != "closed" || reopenBefore["closure_reason"] != "not_applicable" {
		t.Errorf("reopened row before = %v", rows[1]["before"])
	}
	for _, r := range rows {
		if r["actor"] == "" {
			t.Errorf("%v row has no actor", r["action"])
		}
	}
}

// TestLifecycle_RegisterExcludesClosedUnlessAsked: GET /v1/risks and its
// count leave a closed risk out by default and include it with
// ?include_closed=true; the heatmap stops counting it; reopening brings it
// back.
func TestLifecycle_RegisterExcludesClosedUnlessAsked(t *testing.T) {
	env := newTransitionEnv(t)
	open := env.newRisk(t)
	closed := env.newRisk(t)
	heatmapCount := func() int {
		code, body := env.do(t, http.MethodGet, "/v1/risks/heatmap", env.ownerJWT, nil)
		if code != http.StatusOK {
			t.Fatalf("heatmap: status %d: %v", code, body)
		}
		// newRisk scores likelihood 3, impact 4.
		grid := body["grid"].([]any)
		return int(grid[2].([]any)[3].(float64))
	}
	if n := heatmapCount(); n != 2 {
		t.Fatalf("heatmap before close = %d, want 2", n)
	}

	env.step(t, closed, "close", env.ownerJWT, map[string]any{"reason": "realised_and_handled"}, http.StatusOK)

	ids, count := env.listIDs(t, "")
	if !slices.Equal(ids, []string{open}) || count != 1 {
		t.Fatalf("default list = %v (count %d), want only %s", ids, count, open)
	}
	ids, count = env.listIDs(t, "?include_closed=true")
	if len(ids) != 2 || count != 2 || !slices.Contains(ids, closed) {
		t.Fatalf("include_closed list = %v (count %d), want both", ids, count)
	}
	if n := heatmapCount(); n != 1 {
		t.Fatalf("heatmap after close = %d, want 1", n)
	}
	if code, body := env.do(t, http.MethodGet, "/v1/risks?include_closed=maybe", env.ownerJWT, nil); code != http.StatusBadRequest {
		t.Fatalf("include_closed=maybe: status %d, want 400: %v", code, body)
	}

	// The detail page still reads a closed risk.
	if code, body := env.do(t, http.MethodGet, "/v1/risks/"+closed, env.ownerJWT, nil); code != http.StatusOK || riskField(t, body, "status") != "closed" {
		t.Fatalf("GET closed risk: status %d: %v", code, body)
	}

	env.step(t, closed, "reopen", env.ownerJWT, nil, http.StatusOK)
	if _, count = env.listIDs(t, ""); count != 2 {
		t.Fatalf("default count after reopen = %d, want 2", count)
	}
}

// briefIDs reads the board brief's top-risks query as of asOf under the
// tenant, as the board store does.
func (env transitionEnv) briefIDs(t *testing.T, asOf time.Time) []string {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), env.tenant)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	tx, err := env.app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	rows, err := dbx.New(tx).ListRisksForBoardBrief(ctx, dbx.ListRisksForBoardBriefParams{
		TenantID:  pgtype.UUID{Bytes: uuid.MustParse(env.tenant), Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: asOf, Valid: true},
	})
	if err != nil {
		t.Fatalf("ListRisksForBoardBrief: %v", err)
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = uuid.UUID(r.ID.Bytes).String()
	}
	return ids
}

// TestLifecycle_BoardBriefTopRisksExcludeClosed: the board brief's and
// board pack's top-risks read leaves out a risk closed before the brief's
// date, and keeps one closed after it (it was open as of that date).
func TestLifecycle_BoardBriefTopRisksExcludeClosed(t *testing.T) {
	env := newTransitionEnv(t)
	open := env.newRisk(t)
	closed := env.newRisk(t)
	env.step(t, closed, "close", env.ownerJWT, closeReason(), http.StatusOK)

	read := func(asOf time.Time) []string { return env.briefIDs(t, asOf) }

	if got := read(time.Now().UTC().Add(time.Minute)); !slices.Equal(got, []string{open}) {
		t.Fatalf("brief as of now = %v, want only %s", got, open)
	}
	var closedAt time.Time
	if err := env.admin.QueryRow(context.Background(),
		`SELECT closed_at FROM risks WHERE id = $1`, closed).Scan(&closedAt); err != nil {
		t.Fatalf("read closed_at: %v", err)
	}
	if got := read(closedAt.Add(-time.Nanosecond)); !slices.Contains(got, closed) {
		t.Fatalf("brief as of just before the close = %v, want it to include %s", got, closed)
	}
}

// TestLifecycle_HardDeleteIsAdminOnly: grc_engineer and control_owner are
// refused and the risk survives; a tenant admin deletes it, and the
// deleted row stays in risk_audit_log after the risk is gone.
func TestLifecycle_HardDeleteIsAdminOnly(t *testing.T) {
	// The tenant admin is a person with a live admin grant, and authz runs
	// as production wires it (newTransitionEnv attaches the engine): the
	// delete gate reads the live role, not the JWT claim (#2428).
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	tenantAdmin := liveroletest.Bearer(t, env.srv, env.tenant, "admin")

	for name, bearer := range map[string]string{
		"grc_engineer":  env.grcJWT,
		"control_owner": liveroletest.Bearer(t, env.srv, env.tenant, "control_owner"),
		"viewer":        liveroletest.Bearer(t, env.srv, env.tenant, "viewer"),
	} {
		code, body := env.do(t, http.MethodDelete, "/v1/risks/"+id, bearer, nil)
		if code != http.StatusForbidden {
			t.Fatalf("%s delete: status %d, want 403: %v", name, code, body)
		}
	}
	if code, _ := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil); code != http.StatusOK {
		t.Fatalf("risk gone after refused deletes: status %d", code)
	}

	if code, body := env.do(t, http.MethodDelete, "/v1/risks/"+id, tenantAdmin, nil); code != http.StatusNoContent {
		t.Fatalf("tenant admin delete: status %d, want 204: %v", code, body)
	}
	if code, _ := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil); code != http.StatusNotFound {
		t.Fatalf("risk after admin delete: status %d, want 404", code)
	}

	var action, actor, title string
	if err := env.admin.QueryRow(context.Background(), `
		SELECT action_type, actor, before_state->>'title'
		FROM risk_audit_log WHERE risk_id = $1 AND action_type = 'deleted'`, id).
		Scan(&action, &actor, &title); err != nil {
		t.Fatalf("deleted audit row: %v", err)
	}
	if actor == "" || title != "transition risk" {
		t.Fatalf("deleted row actor=%q title=%q", actor, title)
	}
}

// TestLifecycle_ParentOfClosedChildrenIsNotARollUp: a parent whose only
// child is closed summarises nothing the register shows, so it is listed
// and ranked in the board brief itself; reopening the child makes it a
// roll-up again, hidden from both.
func TestLifecycle_ParentOfClosedChildrenIsNotARollUp(t *testing.T) {
	env := newTransitionEnv(t)
	parent := seedLeveledRisk(t, env.admin, env.tenant, "org", "rollup parent", 4, 4).String()
	child := seedLeveledRisk(t, env.admin, env.tenant, "team", "rollup child", 2, 3).String()
	if code, body := env.do(t, http.MethodPost, "/v1/risks/"+parent+"/children", env.ownerJWT,
		map[string]any{"child_risk_ids": []string{child}}); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("link child: status %d: %v", code, body)
	}
	soon := func() time.Time { return time.Now().UTC().Add(time.Minute) }

	ids, _ := env.listIDs(t, "")
	if !slices.Equal(ids, []string{child}) {
		t.Fatalf("register with an open child = %v, want only the child", ids)
	}
	if got := env.briefIDs(t, soon()); !slices.Equal(got, []string{child}) {
		t.Fatalf("brief with an open child = %v, want only the child", got)
	}

	env.step(t, child, "close", env.ownerJWT, closeReason(), http.StatusOK)
	ids, count := env.listIDs(t, "")
	if !slices.Equal(ids, []string{parent}) || count != 1 {
		t.Fatalf("register with the child closed = %v (count %d), want only the parent", ids, count)
	}
	if got := env.briefIDs(t, soon()); !slices.Equal(got, []string{parent}) {
		t.Fatalf("brief with the child closed = %v, want only the parent", got)
	}

	env.step(t, child, "reopen", env.ownerJWT, nil, http.StatusOK)
	if ids, _ = env.listIDs(t, ""); !slices.Equal(ids, []string{child}) {
		t.Fatalf("register after reopen = %v, want only the child", ids)
	}
	if got := env.briefIDs(t, soon()); !slices.Equal(got, []string{child}) {
		t.Fatalf("brief after reopen = %v, want only the child", got)
	}
}

// TestLifecycle_ClosedRiskRefusesTreatmentAndAcceptance: a closed risk
// takes no treatment or acceptance transition (409) and writes no audit
// row for one; reopened, it takes them again.
func TestLifecycle_ClosedRiskRefusesTreatmentAndAcceptance(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	env.step(t, id, "close", env.ownerJWT, closeReason(), http.StatusOK)

	env.step(t, id, "treatment", env.ownerJWT, map[string]any{"treatment": "avoid"}, http.StatusConflict)
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusConflict)
	if got := actionsOf(env.auditRows(t, id)); !slices.Equal(got, []string{"closed"}) {
		t.Fatalf("audit actions = %v, want only [closed]", got)
	}

	env.step(t, id, "reopen", env.ownerJWT, nil, http.StatusOK)
	body := env.step(t, id, "treatment", env.ownerJWT, map[string]any{"treatment": "avoid"}, http.StatusOK)
	if got := riskField(t, body, "treatment"); got != "avoid" {
		t.Fatalf("treatment after reopen = %v, want avoid", got)
	}
}

// TestLifecycle_ClosureNoteIsCapped: a note over 4000 characters is a
// 400 and the risk stays open.
func TestLifecycle_ClosureNoteIsCapped(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	env.step(t, id, "close", env.ownerJWT,
		map[string]any{"reason": "merged", "note": strings.Repeat("x", 4001)}, http.StatusBadRequest)
	env.step(t, id, "close", env.ownerJWT,
		map[string]any{"reason": "merged", "note": strings.Repeat("x", 4000)}, http.StatusOK)
	if _, err := env.admin.Exec(context.Background(),
		`UPDATE risks SET closure_note = repeat('x', 4001) WHERE id = $1`, id); err == nil {
		t.Fatal("a 4001-character note was accepted by the database")
	}
}
