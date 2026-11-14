//go:build integration

// #1748 — integration tests for risk hierarchy links into an existing risk:
// POST/GET /v1/risks/{id}/children, DELETE /v1/risks/{id}/children/{child_id},
// GET /v1/risks/{id}/parents, and the roll-up on GET /v1/risks/{id}. Real
// Postgres with RLS + the assembled platform router.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run Children

package risks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// seedLeveledRisk inserts a nist_800_30 risk at the given level with a
// likelihood × impact score. risk_aggregations rows go with their risks
// (ON DELETE CASCADE), so the tenant cleanup on risks covers them.
func seedLeveledRisk(t *testing.T, admin *pgxpool.Pool, tenant, level, title string, likelihood, impact int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	inherent, _ := json.Marshal(map[string]int{"likelihood": likelihood, "impact": impact})
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score, level
		)
		VALUES ($1, $2, $3, '', 'operational', 'nist_800_30',
		        $4::jsonb, 'untreated', '', '{}'::jsonb, $5::risk_level)
	`, id, tenant, title, string(inherent), level); err != nil {
		t.Fatalf("seed %s risk: %v", level, err)
	}
	return id
}

func freshChildrenTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "risk_aggregations", "risks")
}

func linkChildren(t *testing.T, env testEnv, parent uuid.UUID, children ...uuid.UUID) (*http.Response, map[string]any) {
	t.Helper()
	ids := make([]string, len(children))
	for i, c := range children {
		ids[i] = c.String()
	}
	return post(t, env, "/v1/risks/"+parent.String()+"/children",
		map[string]any{"child_risk_ids": ids}, env.bearer)
}

func unlinkChild(t *testing.T, env testEnv, parent, child uuid.UUID) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete,
		env.server.URL+"/v1/risks/"+parent.String()+"/children/"+child.String(), nil)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE child: %v", err)
	}
	_ = resp.Body.Close()
	return resp
}

// linkedIDs reads the ids out of a children or parents listing.
func linkedIDs(t *testing.T, body map[string]any, key string) map[string]map[string]any {
	t.Helper()
	list, ok := body[key].([]any)
	if !ok {
		t.Fatalf("response has no %s array: %v", key, body)
	}
	out := make(map[string]map[string]any, len(list))
	for _, e := range list {
		m := e.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func linkRowCount(t *testing.T, admin *pgxpool.Pool, tenant string, parent uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_aggregations WHERE tenant_id = $1 AND parent_risk_id = $2`,
		tenant, parent).Scan(&n); err != nil {
		t.Fatalf("count links: %v", err)
	}
	return n
}

// AC: team risks attach under an existing company risk, and the link shows
// in the parent's children and in each child's parents.
func TestChildren_AttachTeamRisksUnderCompanyRisk(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	company := seedLeveledRisk(t, admin, tenant, "company", "enterprise risk", 2, 2)
	teamA := seedLeveledRisk(t, admin, tenant, "team", "team finding A", 3, 4)
	teamB := seedLeveledRisk(t, admin, tenant, "team", "team finding B", 5, 5)

	resp, body := linkChildren(t, env, company, teamA, teamB)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("link status %d, want 200: %v", resp.StatusCode, body)
	}
	children := linkedIDs(t, body, "children")
	if len(children) != 2 {
		t.Fatalf("children = %v, want teamA + teamB", children)
	}
	a := children[teamA.String()]
	if a == nil || a["level"] != "team" || a["link_source"] != "manual" || a["title"] != "team finding A" {
		t.Fatalf("teamA entry = %v, want team/manual/title", a)
	}
	if a["severity"].(float64) != 12 {
		t.Fatalf("teamA severity = %v, want 12", a["severity"])
	}

	// GET listing agrees with the POST response.
	resp, body = get(t, env, "/v1/risks/"+company.String()+"/children")
	if resp.StatusCode != http.StatusOK || len(linkedIDs(t, body, "children")) != 2 {
		t.Fatalf("GET children: %d %v", resp.StatusCode, body)
	}

	for _, child := range []uuid.UUID{teamA, teamB} {
		resp, body := get(t, env, "/v1/risks/"+child.String()+"/parents")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET parents of %s: %d %v", child, resp.StatusCode, body)
		}
		parents := linkedIDs(t, body, "parents")
		p := parents[company.String()]
		if len(parents) != 1 || p == nil || p["level"] != "company" {
			t.Fatalf("parents of %s = %v, want the company risk", child, parents)
		}
	}
}

// AC: re-attaching a linked child is a 2xx no-op with no duplicate row.
func TestChildren_ReattachIsNoOp(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	org := seedLeveledRisk(t, admin, tenant, "org", "org risk", 3, 3)
	team := seedLeveledRisk(t, admin, tenant, "team", "team risk", 2, 3)

	for i := 0; i < 2; i++ {
		resp, body := linkChildren(t, env, org, team, team)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("link attempt %d: status %d, want 200: %v", i, resp.StatusCode, body)
		}
	}
	if n := linkRowCount(t, admin, tenant, org); n != 1 {
		t.Fatalf("link rows = %d, want 1", n)
	}
}

// AC: a child at the same or a higher level than the parent is a 400.
func TestChildren_LevelNotBelowParentIs400(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	org := seedLeveledRisk(t, admin, tenant, "org", "org risk", 3, 3)
	sibling := seedLeveledRisk(t, admin, tenant, "org", "another org risk", 3, 3)
	company := seedLeveledRisk(t, admin, tenant, "company", "company risk", 3, 3)

	for name, child := range map[string]uuid.UUID{"same level": sibling, "higher level": company} {
		resp, body := linkChildren(t, env, org, child)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %v", name, resp.StatusCode, body)
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, child.String()) {
			t.Fatalf("%s: error %q does not name the offending risk %s", name, msg, child)
		}
	}
	if n := linkRowCount(t, admin, tenant, org); n != 0 {
		t.Fatalf("link rows after refusals = %d, want 0", n)
	}
}

// AC: attaching an ancestor of the parent, at any depth, is a 409. The
// levels are moved after the links are made, which is how a real cycle
// risk arises: PATCH /v1/risks/{id} can change a level after a link.
func TestChildren_AncestorAtAnyDepthIs409(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	top := seedLeveledRisk(t, admin, tenant, "company", "top", 3, 3)
	mid := seedLeveledRisk(t, admin, tenant, "org", "mid", 3, 3)
	bottom := seedLeveledRisk(t, admin, tenant, "team", "bottom", 3, 3)
	if resp, body := linkChildren(t, env, top, mid); resp.StatusCode != http.StatusOK {
		t.Fatalf("link top->mid: %d %v", resp.StatusCode, body)
	}
	if resp, body := linkChildren(t, env, mid, bottom); resp.StatusCode != http.StatusOK {
		t.Fatalf("link mid->bottom: %d %v", resp.StatusCode, body)
	}

	// top is bottom's grandparent: depth 2.
	resp, body := linkChildren(t, env, bottom, top)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("grandparent under grandchild: status %d, want 409: %v", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, top.String()) {
		t.Fatalf("409 error %q does not name the offending risk %s", msg, top)
	}
	// Even when the levels would allow it: lift bottom to company and drop
	// top to team, then try again.
	if _, err := admin.Exec(context.Background(),
		`UPDATE risks SET level = 'company' WHERE id = $1`, bottom); err != nil {
		t.Fatalf("lift bottom: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`UPDATE risks SET level = 'team' WHERE id = $1`, top); err != nil {
		t.Fatalf("drop top: %v", err)
	}
	if resp, body := linkChildren(t, env, bottom, top); resp.StatusCode != http.StatusConflict {
		t.Fatalf("cycle with permissive levels: status %d, want 409: %v", resp.StatusCode, body)
	}
	// A risk under itself is a cycle too.
	if resp, body := linkChildren(t, env, mid, mid); resp.StatusCode != http.StatusConflict {
		t.Fatalf("self link: status %d, want 409: %v", resp.StatusCode, body)
	}
	if n := linkRowCount(t, admin, tenant, bottom); n != 0 {
		t.Fatalf("links under bottom = %d, want 0", n)
	}
}

// AC: a risk from another tenant cannot be attached, and RLS holds: the
// other tenant sees neither the link nor the parent.
func TestChildren_CrossTenantRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshChildrenTenant(t, admin)
	tenantB := freshChildrenTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	parentA := seedLeveledRisk(t, admin, tenantA, "company", "A parent", 3, 3)
	childB := seedLeveledRisk(t, admin, tenantB, "team", "B child", 3, 3)

	resp, body := linkChildren(t, envA, parentA, childB)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign child: status %d, want 404: %v", resp.StatusCode, body)
	}
	if n := linkRowCount(t, admin, tenantA, parentA); n != 0 {
		t.Fatalf("links under A's parent = %d, want 0", n)
	}
	// Tenant B cannot reach A's parent at all.
	if resp, body := linkChildren(t, envB, parentA, childB); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("B linking under A's parent: status %d, want 404: %v", resp.StatusCode, body)
	}
	if resp, _ := get(t, envB, "/v1/risks/"+parentA.String()+"/children"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("B reading A's children: status %d, want 404", resp.StatusCode)
	}
	// B's own child lists no parent.
	resp, body = get(t, envB, "/v1/risks/"+childB.String()+"/parents")
	if resp.StatusCode != http.StatusOK || len(linkedIDs(t, body, "parents")) != 0 {
		t.Fatalf("B child parents: %d %v, want none", resp.StatusCode, body)
	}
}

// AC: detaching removes the link; detaching a child that is not linked is
// a 404.
func TestChildren_DetachRemovesLink(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	org := seedLeveledRisk(t, admin, tenant, "org", "org risk", 3, 3)
	team := seedLeveledRisk(t, admin, tenant, "team", "team risk", 2, 3)
	if resp, body := linkChildren(t, env, org, team); resp.StatusCode != http.StatusOK {
		t.Fatalf("link: %d %v", resp.StatusCode, body)
	}

	if resp := unlinkChild(t, env, org, team); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("detach: status %d, want 204", resp.StatusCode)
	}
	if n := linkRowCount(t, admin, tenant, org); n != 0 {
		t.Fatalf("link rows after detach = %d, want 0", n)
	}
	resp, body := get(t, env, "/v1/risks/"+team.String()+"/parents")
	if resp.StatusCode != http.StatusOK || len(linkedIDs(t, body, "parents")) != 0 {
		t.Fatalf("parents after detach: %d %v, want none", resp.StatusCode, body)
	}
	if resp := unlinkChild(t, env, org, team); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("detach unlinked child: status %d, want 404", resp.StatusCode)
	}
}

// AC: linking and unlinking leave the parent's stored inherent_score and
// treatment byte-for-byte unchanged.
func TestChildren_ParentScoreAndTreatmentUnchanged(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	company := seedLeveledRisk(t, admin, tenant, "company", "enterprise risk", 2, 2)
	team := seedLeveledRisk(t, admin, tenant, "team", "team finding", 5, 5)
	if _, err := admin.Exec(context.Background(),
		`UPDATE risks SET treatment = 'avoid', treatment_owner = 'ciso' WHERE id = $1`, company); err != nil {
		t.Fatalf("set treatment: %v", err)
	}
	read := func() (inherent, treatment, owner string) {
		t.Helper()
		if err := admin.QueryRow(context.Background(), `
			SELECT inherent_score::text, treatment::text, treatment_owner
			FROM risks WHERE tenant_id = $1 AND id = $2`, tenant, company,
		).Scan(&inherent, &treatment, &owner); err != nil {
			t.Fatalf("read parent: %v", err)
		}
		return
	}
	inherent0, treatment0, owner0 := read()

	if resp, body := linkChildren(t, env, company, team); resp.StatusCode != http.StatusOK {
		t.Fatalf("link: %d %v", resp.StatusCode, body)
	}
	if i, tr, o := read(); i != inherent0 || tr != treatment0 || o != owner0 {
		t.Fatalf("after link: (%q,%q,%q), want (%q,%q,%q)", i, tr, o, inherent0, treatment0, owner0)
	}
	if resp := unlinkChild(t, env, company, team); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("detach: status %d", resp.StatusCode)
	}
	if i, tr, o := read(); i != inherent0 || tr != treatment0 || o != owner0 {
		t.Fatalf("after unlink: (%q,%q,%q), want (%q,%q,%q)", i, tr, o, inherent0, treatment0, owner0)
	}
}

// AC: GET /v1/risks/{id} carries the roll-up beside the assessed score,
// with the higher-than-assessed flag, and omits it for a risk with no
// children.
func TestChildren_GetRiskCarriesRollUp(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	company := seedLeveledRisk(t, admin, tenant, "company", "enterprise risk", 2, 2) // assessed 4
	teamA := seedLeveledRisk(t, admin, tenant, "team", "A", 3, 4)                    // 12
	teamB := seedLeveledRisk(t, admin, tenant, "team", "B", 4, 4)                    // 16

	resp, body := get(t, env, "/v1/risks/"+company.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET risk: %d %v", resp.StatusCode, body)
	}
	if _, has := body["risk"].(map[string]any)["rollup"]; has {
		t.Fatalf("risk with no children carries a rollup: %v", body)
	}

	if resp, body := linkChildren(t, env, company, teamA, teamB); resp.StatusCode != http.StatusOK {
		t.Fatalf("link: %d %v", resp.StatusCode, body)
	}
	resp, body = get(t, env, "/v1/risks/"+company.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET risk: %d %v", resp.StatusCode, body)
	}
	rk := body["risk"].(map[string]any)
	ru, ok := rk["rollup"].(map[string]any)
	if !ok {
		t.Fatalf("risk with children has no rollup: %v", rk)
	}
	if ru["severity"].(float64) != 16 || ru["severity_function"] != "max" ||
		ru["child_count"].(float64) != 2 || ru["assessed_severity"].(float64) != 4 ||
		ru["exceeds_assessed"] != true {
		t.Fatalf("rollup = %v, want severity 16 via max over 2 children, assessed 4, exceeds", ru)
	}
	if rk["severity"].(float64) != 4 {
		t.Fatalf("assessed severity = %v, want 4 (unchanged by the roll-up)", rk["severity"])
	}
}

// Links are serialized per tenant: while another transaction holds the
// tenant's hierarchy lock, a link waits, and it completes once the lock is
// released (#1748). That is what stops two concurrent links from each
// passing the cycle check on a snapshot that lacks the other.
func TestChildren_LinkWaitsForHierarchyLock(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	org := seedLeveledRisk(t, admin, tenant, "org", "org risk", 3, 3)
	team := seedLeveledRisk(t, admin, tenant, "team", "team risk", 2, 3)

	ctx := context.Background()
	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('risk_hierarchy:' || $1::uuid::text, 0))`, tenant); err != nil {
		t.Fatalf("take hierarchy lock: %v", err)
	}

	done := make(chan int, 1)
	go func() {
		resp, _ := linkChildren(t, env, org, team)
		done <- resp.StatusCode
	}()
	select {
	case status := <-done:
		t.Fatalf("link finished (status %d) while the hierarchy lock was held", status)
	case <-time.After(500 * time.Millisecond):
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release hierarchy lock: %v", err)
	}
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("link after release: status %d, want 200", status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("link did not finish after the hierarchy lock was released")
	}
}

// One request may name at most risk.MaxChildrenPerLink children.
func TestChildren_TooManyChildrenIs400(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)
	org := seedLeveledRisk(t, admin, tenant, "org", "org risk", 3, 3)

	ids := make([]uuid.UUID, risk.MaxChildrenPerLink+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if resp, body := linkChildren(t, env, org, ids...); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %v", resp.StatusCode, body)
	}
}
