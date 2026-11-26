//go:build integration

// #2172 — module access for each member. Each request goes through the
// production router (JWT, tenancy, the module gate, the module access
// gate, OPA with the live user_roles resolver) on real Postgres under RLS.
// Members are real users rows with user_roles, signed in with a
// "user:<uuid>" subject; access is set through
// PUT /v1/admin/users/{id}/module-access as an admin would.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type moduleAccessEnv struct {
	*moduleGateFixture
	srv          *api.Server
	tenant       string
	migrate, app *pgxpool.Pool
}

// newModuleAccessEnv builds the production router for a fresh tenant. The
// fixture's admin is the operator (an admin credential) that flips switches
// and sets access; members come from e.member.
func newModuleAccessEnv(t *testing.T) *moduleAccessEnv {
	t.Helper()
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate,
		"member_module_access", "feature_flags", "feature_flag_audit_log",
		"questionnaires", "decision_audit_log")
	tid := uuid.MustParse(tenant)

	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	f := &moduleGateFixture{admin: srv.IssueTestJWT(t, testjwt.AdminFor(tid))}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	f.url = ts.URL
	return &moduleAccessEnv{moduleGateFixture: f, srv: srv, tenant: tenant, migrate: migrate, app: app}
}

// member seeds a users row holding role and returns its id and a bearer
// for it. The claim carries no super-admin flag, so the role comes only
// from user_roles.
func (e *moduleAccessEnv) member(t *testing.T, role string) (uuid.UUID, string) {
	t.Helper()
	u := seedPeriodUser(t, e.migrate, e.tenant, "active", role)
	c := testjwt.OwnerFor(uuid.MustParse(e.tenant), []string{role})
	c.Subject = "user:" + u.String()
	return u, e.srv.IssueTestJWT(t, c)
}

// setAccess sets a member's module access as the operator; nil is "all".
func (e *moduleAccessEnv) setAccess(t *testing.T, user uuid.UUID, modules []string) {
	t.Helper()
	got := e.do(t, http.MethodPut, "/v1/admin/users/"+user.String()+"/module-access", e.admin, map[string]any{"modules": modules})
	if got.status != http.StatusOK {
		t.Fatalf("PUT module-access %v: status %d body=%s", modules, got.status, got.body)
	}
}

// noAccess reports whether a response is the module-access 403 naming
// module; module "" matches the 403 naming any module.
func noAccess(r moduleGateResponse, module string) bool {
	if r.status != http.StatusForbidden {
		return false
	}
	var body struct {
		Module string `json:"module"`
	}
	if json.Unmarshal([]byte(r.body), &body) != nil || body.Module == "" {
		return false
	}
	return module == "" || body.Module == module
}

// A viewer without Questionnaires access gets 403 naming the module; with
// access they get today's role decision; with Questionnaires switched off
// they get 404 with the switched-off marker.
func TestModuleAccess_ViewerNoAccessThenRoleDecisionThenSwitchedOff(t *testing.T) {
	e := newModuleAccessEnv(t)
	_, wildcard := e.member(t, "viewer")
	narrowed, viewer := e.member(t, "viewer")

	// Today's role decision, from a viewer on "all". viewer.rego admits no
	// questionnaire read or write, so both are the OPA 403 "forbidden",
	// which does not name a module.
	todayRead := e.do(t, http.MethodGet, "/v1/questionnaires", wildcard, nil)
	todayWrite := e.do(t, http.MethodPost, "/v1/questionnaires", wildcard, map[string]any{"name": "#2172", "source_label": "test"})
	for _, today := range []moduleGateResponse{todayRead, todayWrite} {
		if today.status != http.StatusForbidden || today.body != "{\"error\":\"forbidden\"}\n" {
			t.Fatalf("wildcard viewer: status %d body=%q; want the role decision's 403 forbidden", today.status, today.body)
		}
	}

	e.setAccess(t, narrowed, []string{"policies"})
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/v1/questionnaires"},
		{http.MethodGet, "/v1/questionnaires/" + uuid.NewString()},
		{http.MethodPost, "/v1/questionnaires"},
	} {
		got := e.do(t, req.method, req.path, viewer, map[string]any{})
		if !noAccess(got, "questionnaires") {
			t.Fatalf("no access: %s %s: status %d body=%s; want 403 naming questionnaires", req.method, req.path, got.status, got.body)
		}
	}
	if got := e.modules(t, viewer)["questionnaires"]; got != "no_access" {
		t.Fatalf("no access: /v1/me/modules questionnaires = %q; want no_access", got)
	}
	// Policies, inside the set, keeps the role decision.
	if got := e.do(t, http.MethodGet, "/v1/policies", viewer, nil); got.status != http.StatusOK {
		t.Fatalf("policies (in access): status %d body=%s; want 200", got.status, got.body)
	}

	e.setAccess(t, narrowed, []string{"policies", "questionnaires"})
	if got := e.do(t, http.MethodGet, "/v1/questionnaires", viewer, nil); got.status != todayRead.status || got.body != todayRead.body {
		t.Fatalf("with access: GET status %d body=%s; want the role decision %d %s", got.status, got.body, todayRead.status, todayRead.body)
	}
	got := e.do(t, http.MethodPost, "/v1/questionnaires", viewer, map[string]any{"name": "#2172", "source_label": "test"})
	if got.status != todayWrite.status || got.body != todayWrite.body {
		t.Fatalf("with access: POST status %d body=%s; want the role decision %d %s", got.status, got.body, todayWrite.status, todayWrite.body)
	}
	if got := e.modules(t, viewer)["questionnaires"]; got != "on" {
		t.Fatalf("with access: /v1/me/modules questionnaires = %q; want on", got)
	}

	e.setAccess(t, narrowed, []string{"policies"})
	e.setSwitch(t, "questionnaires.enabled", false)
	for _, bearer := range []string{viewer, wildcard} {
		if got := e.do(t, http.MethodGet, "/v1/questionnaires", bearer, nil); !isMarker(got, "questionnaires.enabled") {
			t.Fatalf("switched off: status %d marker %q body=%s; want the 404 marker", got.status, got.marker, got.body)
		}
	}
	if got := e.modules(t, viewer)["questionnaires"]; got != "switched_off" {
		t.Fatalf("switched off: /v1/me/modules questionnaires = %q; want switched_off (off wins over no access)", got)
	}
}

// An admin with a narrowed stored value still reaches every switched-on
// module; an auditor's answers are unchanged by module access.
func TestModuleAccess_AdminAndAuditorUnaffected(t *testing.T) {
	e := newModuleAccessEnv(t)
	admin, adminBearer := e.member(t, "admin")
	auditor, auditorBearer := e.member(t, "auditor")
	routes := []string{"/v1/questionnaires", "/v1/risks", "/v1/policies", "/v1/vendors", "/v1/exceptions", "/v1/personnel-security/checklists"}

	before := map[string]int{}
	for _, p := range routes {
		before[p] = e.do(t, http.MethodGet, p, auditorBearer, nil).status
	}

	e.setAccess(t, admin, []string{})
	e.setAccess(t, auditor, []string{})

	for _, p := range routes {
		if got := e.do(t, http.MethodGet, p, adminBearer, nil); got.status != http.StatusOK {
			t.Fatalf("narrowed admin: GET %s: status %d body=%s; want 200", p, got.status, got.body)
		}
		got := e.do(t, http.MethodGet, p, auditorBearer, nil)
		if got.status != before[p] || noAccess(got, "") {
			t.Fatalf("narrowed auditor: GET %s: status %d body=%s; want %d as before", p, got.status, got.body, before[p])
		}
	}
	for key, state := range e.modules(t, adminBearer) {
		if key != "board_reporting" && state != "on" {
			t.Fatalf("narrowed admin: /v1/me/modules %s = %q; want on", key, state)
		}
	}
	for key, state := range e.modules(t, auditorBearer) {
		if state == "no_access" {
			t.Fatalf("narrowed auditor: /v1/me/modules %s = no_access; module access does not apply to auditors", key)
		}
	}

	// An admin cannot narrow their own access.
	if got := e.do(t, http.MethodPut, "/v1/admin/users/"+admin.String()+"/module-access", adminBearer, map[string]any{"modules": []string{"policies"}}); got.status != http.StatusBadRequest {
		t.Fatalf("self-narrow: status %d body=%s; want 400", got.status, got.body)
	}
	// And a non-admin cannot set anyone's.
	_, viewer := e.member(t, "viewer")
	if got := e.do(t, http.MethodPut, "/v1/admin/users/"+auditor.String()+"/module-access", viewer, map[string]any{"modules": nil}); got.status != http.StatusForbidden {
		t.Fatalf("viewer sets access: status %d; want 403", got.status)
	}
}

// A member on the wildcard gains a module as soon as it is switched on; a
// member with an explicit set does not.
func TestModuleAccess_WildcardGainsNewlySwitchedOnModule(t *testing.T) {
	e := newModuleAccessEnv(t)
	_, wildcard := e.member(t, "grc_engineer")
	explicit, explicitBearer := e.member(t, "grc_engineer")
	e.setAccess(t, explicit, []string{"policies", "questionnaires"})

	// Board reporting is off by default.
	for _, b := range []string{wildcard, explicitBearer} {
		if got := e.modules(t, b)["board_reporting"]; got != "switched_off" {
			t.Fatalf("before: board_reporting = %q; want switched_off", got)
		}
	}
	e.setSwitch(t, "board.reporting", true)

	if got := e.modules(t, wildcard)["board_reporting"]; got != "on" {
		t.Fatalf("wildcard: board_reporting = %q; want on", got)
	}
	if got := e.do(t, http.MethodGet, "/v1/board-packs", wildcard, nil); got.status != http.StatusOK {
		t.Fatalf("wildcard: GET /v1/board-packs: status %d body=%s; want 200", got.status, got.body)
	}
	if got := e.modules(t, explicitBearer)["board_reporting"]; got != "no_access" {
		t.Fatalf("explicit: board_reporting = %q; want no_access", got)
	}
	if got := e.do(t, http.MethodGet, "/v1/board-packs", explicitBearer, nil); !noAccess(got, "board_reporting") {
		t.Fatalf("explicit: GET /v1/board-packs: status %d body=%s; want 403 naming board_reporting", got.status, got.body)
	}

	// Back to "all": the module is reachable again.
	e.setAccess(t, explicit, nil)
	if got := e.modules(t, explicitBearer)["board_reporting"]; got != "on" {
		t.Fatalf("explicit reset to all: board_reporting = %q; want on", got)
	}
}

// #2466: a request the access gate refuses writes one decision_audit_log
// row, the module gate's shape (deny, resource "module", the actor) with
// reason "module_no_access", and a polling client's repeats inside the
// window write no more.
func TestModuleAccess_NoAccessAttemptIsAudited(t *testing.T) {
	e := newModuleAccessEnv(t)
	narrowed, viewer := e.member(t, "viewer")
	e.setAccess(t, narrowed, []string{"policies"})

	for i := 0; i < 3; i++ {
		if got := e.do(t, http.MethodGet, "/v1/questionnaires", viewer, nil); !noAccess(got, "questionnaires") {
			t.Fatalf("GET /v1/questionnaires: status %d body=%s; want 403 naming questionnaires", got.status, got.body)
		}
	}

	rows, err := e.migrate.Query(context.Background(), `
		SELECT user_id, result, resource_type, resource_id, policy_hits, request_path
		  FROM decision_audit_log
		 WHERE tenant_id = $1::uuid AND reason = 'module_no_access'`, e.tenant)
	if err != nil {
		t.Fatalf("query decision_audit_log: %v", err)
	}
	defer rows.Close()
	type row struct {
		user, result, rtype, rid, path string
		hits                           []string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.user, &r.result, &r.rtype, &r.rid, &r.hits, &r.path); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("module_no_access rows = %d (%+v); want 1 for three requests", len(got), got)
	}
	r := got[0]
	if r.user != "user:"+narrowed.String() || r.result != "deny" || r.rtype != "module" || r.rid != "questionnaires" ||
		r.path != "/v1/questionnaires" || len(r.hits) != 1 || r.hits[0] != "module_access:questionnaires" {
		t.Fatalf("row %+v; want the member's deny on module questionnaires", r)
	}
}

// Module-access rows are invisible across tenants (RLS), a member of
// another tenant cannot be targeted, and every change writes one row to the
// append-only access audit log.
func TestModuleAccess_RLSAndAuditLog(t *testing.T) {
	a := newModuleAccessEnv(t)
	b := newModuleAccessEnv(t)
	user, _ := a.member(t, "viewer")

	a.setAccess(t, user, []string{"policies"})
	a.setAccess(t, user, []string{"policies", "risk"})
	a.setAccess(t, user, nil)

	count := func(tenant, table string) int {
		t.Helper()
		ctx := dbtest.WithTenantCtx(t, tenant)
		tx, err := a.app.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			t.Fatalf("apply tenant: %v", err)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE user_id = $1", user.String()).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	if n := count(a.tenant, "member_module_access"); n != 1 {
		t.Fatalf("own tenant: member_module_access rows = %d; want 1", n)
	}
	if n := count(a.tenant, "member_module_access_audit_log"); n != 3 {
		t.Fatalf("own tenant: audit rows = %d; want 3 (one per change)", n)
	}
	if n := count(b.tenant, "member_module_access"); n != 0 {
		t.Fatalf("other tenant: member_module_access rows = %d; want 0 (RLS)", n)
	}
	if n := count(b.tenant, "member_module_access_audit_log"); n != 0 {
		t.Fatalf("other tenant: audit rows = %d; want 0 (RLS)", n)
	}

	// The other tenant's admin cannot set this member's access.
	path := "/v1/admin/users/" + user.String() + "/module-access"
	if got := b.do(t, http.MethodPut, path, b.admin, map[string]any{"modules": []string{}}); got.status != http.StatusNotFound {
		t.Fatalf("other tenant PUT: status %d; want 404", got.status)
	}

	// The audit trail is append-only, even for the migrate role.
	if _, err := a.migrate.Exec(context.Background(),
		`UPDATE member_module_access_audit_log SET actor = 'x' WHERE tenant_id = $1`, a.tenant); err == nil {
		t.Fatal("UPDATE on member_module_access_audit_log succeeded; want the append-only trigger to refuse")
	}

	// Before -> after, and it reaches the unified audit log as module_access.
	window := "from=" + time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	got := a.do(t, http.MethodGet, "/v1/admin/audit-log/unified?kind=module_access&"+window, a.admin, nil)
	if got.status != http.StatusOK {
		t.Fatalf("unified audit log: status %d body=%s", got.status, got.body)
	}
	var log struct {
		Entries []struct {
			Kind     string `json:"kind"`
			TargetID string `json:"target_id"`
			Action   string `json:"action"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(got.body), &log); err != nil {
		t.Fatalf("decode unified log: %v; body=%s", err, got.body)
	}
	n := 0
	for _, it := range log.Entries {
		if it.Kind == "module_access" && it.TargetID == user.String() && it.Action == "module_access.set" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("unified log module_access rows for the member = %d; want 3; body=%s", n, got.body)
	}

	// An unknown module key, or a missing field, is refused.
	if got := a.do(t, http.MethodPut, path, a.admin, map[string]any{"modules": []string{"controls"}}); got.status != http.StatusBadRequest {
		t.Fatalf("core module in set: status %d; want 400", got.status)
	}
	if got := a.do(t, http.MethodPut, path, a.admin, map[string]any{}); got.status != http.StatusBadRequest {
		t.Fatalf("missing modules: status %d; want 400", got.status)
	}
}
