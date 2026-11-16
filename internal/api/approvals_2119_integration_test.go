//go:build integration

// #2119 — GET /v1/approvals end to end through the production router (JWT,
// tenancy, OPA with the DB roles resolver, ModuleGate) on real Postgres
// under RLS.
//
// One requester files a pending record of each of the four types. The
// matrix pins:
//
//   - a second approver sees all four, unmarked, with a count of one each;
//   - the requester sees none while Allow self-approval is off, and each
//     type's approve route refuses them (the list and the gate agree);
//   - with the setting on, the requester sees all four marked
//     self_approval, and the approve route accepts what is listed;
//   - a viewer, and a credential with no person behind it, get an empty
//     list;
//   - another tenant's pending records never appear or count;
//   - an approved record leaves the list.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
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
)

// pending2119 is one requester's four pending records.
type pending2119 struct {
	exception, risk, policy, scope uuid.UUID
}

func seed2119Tenant(t *testing.T, migrate *pgxpool.Pool) string {
	t.Helper()
	tenant := dbtest.SeedTenant(t, migrate)
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO tenants (id, name, allow_self_approval) VALUES ($1, $2, false)`,
		tenant, "approvals-2119-"+tenant[:8]); err != nil {
		t.Fatalf("seed tenants row: %v", err)
	}
	return tenant
}

func set2119SelfApproval(t *testing.T, migrate *pgxpool.Pool, tenant string, on bool) {
	t.Helper()
	if _, err := migrate.Exec(context.Background(),
		`UPDATE tenants SET allow_self_approval = $2 WHERE id = $1`, tenant, on); err != nil {
		t.Fatalf("set allow_self_approval: %v", err)
	}
}

// seed2119Pending writes, as requester, a requested exception, a proposed
// risk acceptance, a policy under review and a framework scope in review,
// plus one record of each type that is not pending.
func seed2119Pending(t *testing.T, migrate *pgxpool.Pool, tenant string, requester uuid.UUID) pending2119 {
	t.Helper()
	ctx := context.Background()
	p := pending2119{exception: uuid.New(), risk: uuid.New(), policy: uuid.New(), scope: uuid.New()}
	ctl, fw, fv := uuid.New(), uuid.New(), uuid.New()
	by := requester.String()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO controls (id, tenant_id, title, control_family, implementation_type,
		                        bundle_id, freshness_class, evidence_queries, applicability_expr)
		  VALUES ($1, $2, '#2119 control', 'AST', 'automated', $3, 'monthly', '[]'::jsonb, 'true')`,
			[]any{ctl, tenant, "bundle-2119-" + ctl.String()}},
		{`INSERT INTO exceptions (id, tenant_id, control_id, scope_cell_predicate, justification, requested_by, expires_at)
		  VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, '#2119 pending', $4, now() + interval '30 days')`,
			[]any{p.exception, tenant, ctl, by}},
		{`INSERT INTO exceptions (id, tenant_id, control_id, scope_cell_predicate, justification, requested_by, expires_at, status)
		  VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, '#2119 denied', $4, now() + interval '30 days', 'denied')`,
			[]any{uuid.New(), tenant, ctl, by}},
		{`INSERT INTO risks (id, tenant_id, title, category_id, treatment, acceptance_status,
		                     acceptance_justification, acceptance_proposed_until, acceptance_proposed_by, acceptance_proposed_at)
		  VALUES ($1, $2, '#2119 risk', risk_category_id($2, 'operational'), 'accept', 'proposed',
		          'cost of control exceeds exposure', CURRENT_DATE + 90, $3, now())`,
			[]any{p.risk, tenant, by}},
		{`INSERT INTO risks (id, tenant_id, title, category_id) VALUES ($1, $2, '#2119 untreated', risk_category_id($2, 'operational'))`,
			[]any{uuid.New(), tenant}},
		{`INSERT INTO policies (id, tenant_id, title, version, body_md, status, owner_role, approver_role, created_by, submitted_by, submitted_at)
		  VALUES ($1, $2, '#2119 policy', '1.0', '# Access', 'under_review', 'grc_engineer', 'admin', $3, $3, now())`,
			[]any{p.policy, tenant, by}},
		{`INSERT INTO policies (id, tenant_id, title, version, body_md, status, owner_role, approver_role, created_by)
		  VALUES ($1, $2, '#2119 draft', '1.0', '# Draft', 'draft', 'grc_engineer', 'admin', $3)`,
			[]any{uuid.New(), tenant, by}},
		{`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, '#2119 framework', $2, 'test')`,
			[]any{fw, "test-2119-" + fw.String()}},
		{`INSERT INTO framework_versions (id, tenant_id, framework_id, version) VALUES ($1, NULL, $2, '1.0')`,
			[]any{fv, fw}},
		{`INSERT INTO framework_scopes (id, tenant_id, framework_version_id, name, state, predicate, predicate_hash, submitted_by)
		  VALUES ($1, $2, $3, '#2119 scope', 'review', '{"op":"true"}'::jsonb, $4, $5)`,
			[]any{p.scope, tenant, fv, "hash-2119-" + p.scope.String(), by}},
		{`INSERT INTO framework_scopes (id, tenant_id, framework_version_id, name, state, predicate, predicate_hash)
		  VALUES ($1, $2, $3, '#2119 draft scope', 'draft', '{"op":"true"}'::jsonb, $4)`,
			[]any{uuid.New(), tenant, fv, "hash-2119-draft-" + p.scope.String()}},
	} {
		if _, err := migrate.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed pending: %v\n%s", err, q.sql)
		}
	}
	return p
}

// approve2119 is each type's approve call for p.
func approve2119(p pending2119) map[string]struct {
	path string
	body any
} {
	return map[string]struct {
		path string
		body any
	}{
		"exception":       {"/v1/exceptions/" + p.exception.String() + "/approve", nil},
		"risk_acceptance": {"/v1/risks/" + p.risk.String() + "/acceptance/approve", map[string]any{"accepted_until": time.Now().AddDate(0, 3, 0).Format(time.DateOnly)}},
		"policy":          {"/v1/policies/" + p.policy.String() + "/approve", nil},
		"framework_scope": {"/v1/framework-scopes/" + p.scope.String() + "/approve", nil},
	}
}

// list2119 calls GET /v1/approvals and returns items keyed by type.
func list2119(t *testing.T, c periodAuditorsCall) (map[string]map[string]any, map[string]any) {
	t.Helper()
	st, out := c.do(http.MethodGet, "/v1/approvals", nil)
	if st != http.StatusOK {
		t.Fatalf("GET /v1/approvals: %d %v", st, out)
	}
	items, _ := out["items"].([]any)
	byType := map[string]map[string]any{}
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		typ, _ := it["type"].(string)
		if _, dup := byType[typ]; dup {
			t.Fatalf("two %s items listed: %v", typ, items)
		}
		byType[typ] = it
	}
	return byType, out
}

func TestApprovals2119_ListMatchesApproveGate(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seed2119Tenant(t, migrate)
	tid := uuid.MustParse(tenant)

	requester := seedPeriodUser(t, migrate, tenant, "active", "grc_engineer")
	approver := seedPeriodUser(t, migrate, tenant, "active", "grc_engineer")
	viewer := seedPeriodUser(t, migrate, tenant, "active", "viewer")
	p := seed2119Pending(t, migrate, tenant, requester)

	// Another tenant's pending records, requested by someone else: an
	// approver here must never see or count them.
	otherTenant := seed2119Tenant(t, migrate)
	otherRequester := seedPeriodUser(t, migrate, otherTenant, "active", "grc_engineer")
	seed2119Pending(t, migrate, otherTenant, otherRequester)

	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	bearer := func(u uuid.UUID, role string) string {
		c := testjwt.OwnerFor(tid, []string{role})
		c.Subject = "user:" + u.String()
		return srv.IssueTestJWT(t, c)
	}
	asRequester := periodAuditorsCall{t, "", bearer(requester, "grc_engineer")}
	asApprover := periodAuditorsCall{t, "", bearer(approver, "grc_engineer")}
	asViewer := periodAuditorsCall{t, "", bearer(viewer, "viewer")}
	// An admin credential with no person behind it: it passes the role
	// gate but only people approve (#2101).
	asNobody := periodAuditorsCall{t, "", srv.IssueTestJWT(t, testjwt.AdminFor(tid))}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	for _, c := range []*periodAuditorsCall{&asRequester, &asApprover, &asViewer, &asNobody} {
		c.url = ts.URL
	}
	types := []string{"exception", "risk_acceptance", "policy", "framework_scope"}
	ids := map[string]string{
		"exception": p.exception.String(), "risk_acceptance": p.risk.String(),
		"policy": p.policy.String(), "framework_scope": p.scope.String(),
	}
	approves := approve2119(p)

	// A second person sees all four, unmarked, and nothing of the other
	// tenant.
	got, out := list2119(t, asApprover)
	if len(got) != 4 || out["total"] != float64(4) {
		t.Fatalf("approver sees %d items, total %v; want 4: %v", len(got), out["total"], out)
	}
	counts, _ := out["counts"].(map[string]any)
	for _, typ := range types {
		it, ok := got[typ]
		if !ok {
			t.Fatalf("approver does not see the pending %s: %v", typ, out)
		}
		if it["id"] != ids[typ] || it["self_approval"] != false || it["requested_by"] != requester.String() {
			t.Fatalf("approver's %s item = %v", typ, it)
		}
		if counts[typ] != float64(1) {
			t.Fatalf("counts[%s] = %v, want 1", typ, counts[typ])
		}
	}

	// The requester sees none with the setting off, and every approve
	// route refuses them.
	if got, out := list2119(t, asRequester); len(got) != 0 || out["total"] != float64(0) {
		t.Fatalf("requester sees their own requests with self-approval off: %v", out)
	}
	for _, typ := range types {
		a := approves[typ]
		if st, body := asRequester.do(http.MethodPatch, a.path, a.body); st != http.StatusForbidden {
			t.Fatalf("requester approve %s with self-approval off: %d %v, want 403", typ, st, body)
		}
	}

	// A viewer and a credential with no person list nothing.
	for name, c := range map[string]periodAuditorsCall{"viewer": asViewer, "no person": asNobody} {
		if got, out := list2119(t, c); len(got) != 0 || out["total"] != float64(0) {
			t.Fatalf("%s sees pending approvals: %v", name, out)
		}
	}

	// With the setting on, the requester sees all four, marked.
	set2119SelfApproval(t, migrate, tenant, true)
	got, out = list2119(t, asRequester)
	if len(got) != 4 {
		t.Fatalf("requester with self-approval on sees %d items, want 4: %v", len(got), out)
	}
	for _, typ := range types {
		if got[typ]["self_approval"] != true {
			t.Fatalf("requester's own %s is not marked self_approval: %v", typ, got[typ])
		}
	}
	// The approver's view is unchanged and unmarked.
	if got, _ := list2119(t, asApprover); len(got) != 4 || got["policy"]["self_approval"] != false {
		t.Fatalf("approver's view changed with self-approval on: %v", got)
	}

	// The approve gate accepts what is listed: the requester self-approves
	// two, the second approver the other two.
	for _, step := range []struct {
		typ string
		c   periodAuditorsCall
	}{
		{"exception", asRequester}, {"policy", asRequester},
		{"risk_acceptance", asApprover}, {"framework_scope", asApprover},
	} {
		a := approves[step.typ]
		if st, body := step.c.do(http.MethodPatch, a.path, a.body); st != http.StatusOK {
			t.Fatalf("approve listed %s: %d %v, want 200", step.typ, st, body)
		}
		// It leaves both lists.
		for name, c := range map[string]periodAuditorsCall{"requester": asRequester, "approver": asApprover} {
			if got, _ := list2119(t, c); got[step.typ] != nil {
				t.Fatalf("approved %s still listed for the %s: %v", step.typ, name, got[step.typ])
			}
		}
	}
	for name, c := range map[string]periodAuditorsCall{"requester": asRequester, "approver": asApprover} {
		if got, out := list2119(t, c); len(got) != 0 || out["total"] != float64(0) {
			t.Fatalf("%s still sees approvals after all four were approved: %v", name, out)
		}
	}
}
