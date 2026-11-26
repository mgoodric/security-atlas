//go:build integration

// Issue #2392 — GET /v1/controls/{id}/anchor-history over real Postgres
// with RLS and the assembled router (real OPA bundle, live user_roles).
//
// What this pins:
//   - every remap decision on any revision of the bundle is returned, newest
//     first, from either revision's id; a system principal's decision reads
//     back as that principal;
//   - keyset paging: limit=1 walks both rows with no overlap;
//   - a viewer reads it (OPA and the handler gate agree);
//   - another tenant's control, and an unknown id, are a 404.

package controldetail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// anchorHistoryFixture is one bundle with two revisions: v1 on the legacy
// anchor, v2 the revision an approval re-anchored it to.
type anchorHistoryFixture struct {
	tenant     string
	v1, v2     uuid.UUID
	rejectedAt time.Time
	approvedAt time.Time
	approver   string
}

// seedAnchorCatalog creates a throwaway framework with two releases, one
// anchor each, and removes it after the tenant rows are gone (cleanups run
// last-registered first, so call it before dbtest.SeedTenant).
func seedAnchorCatalog(t *testing.T, admin *pgxpool.Pool) (oldAnchor, newAnchor, newVersion uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fw, oldV := uuid.New(), uuid.New()
	newVersion, oldAnchor, newAnchor = uuid.New(), uuid.New(), uuid.New()
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO frameworks (id, tenant_id, slug, name, issuer) VALUES ($1, NULL, $2, 'test 2392', 'test')`, []any{fw, "test-2392-" + fw.String()[:8]}},
		{`INSERT INTO framework_versions (id, framework_id, version) VALUES ($1, $3, 'old'), ($2, $3, 'new')`, []any{oldV, newVersion, fw}},
		{`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title) VALUES ($1, $2, 'IAC-01', 'IAC', 'old'), ($3, $4, 'IAC-01.1', 'IAC', 'new')`, []any{oldAnchor, oldV, newAnchor, newVersion}},
	}
	for _, s := range stmts {
		if _, err := admin.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed catalog: %v", err)
		}
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = $1`, fw); err != nil {
			t.Errorf("cleanup framework: %v", err)
		}
	})
	return oldAnchor, newAnchor, newVersion
}

func seedAnchorHistory(t *testing.T, admin *pgxpool.Pool) anchorHistoryFixture {
	t.Helper()
	ctx := context.Background()
	oldAnchor, newAnchor, newVersion := seedAnchorCatalog(t, admin)
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "scf_remap_proposals", "controls")
	fx := anchorHistoryFixture{
		tenant: tenant, v1: uuid.New(), v2: uuid.New(),
		rejectedAt: time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond),
		approvedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond),
		approver:   uuid.NewString(),
	}
	bundle := "test-bundle-2392-" + fx.v1.String()[:8]
	insertRevision := func(id uuid.UUID, version int, anchor uuid.UUID, supersededBy *uuid.UUID) {
		if _, err := admin.Exec(ctx, `
			INSERT INTO controls (id, tenant_id, bundle_id, version, superseded_by, scf_anchor_id,
			                      title, control_family, implementation_type, evidence_queries, applicability_expr)
			VALUES ($1, $2, $3, $4, $5, $6, 'anchor history control', 'IAC', 'automated', '[]'::jsonb, 'true')`,
			id, tenant, bundle, version, supersededBy, anchor); err != nil {
			t.Fatalf("seed control v%d: %v", version, err)
		}
	}
	insertRevision(fx.v2, 2, newAnchor, nil)
	insertRevision(fx.v1, 1, oldAnchor, &fx.v2)

	var proposal uuid.UUID
	if err := admin.QueryRow(ctx, `
		INSERT INTO scf_remap_proposals
			(tenant_id, control_id, bundle_id, legacy_anchor_id, legacy_scf_id, legacy_release,
			 target_framework_version_id, target_release, class, status, successor_scf_ids,
			 target_anchor_id, source)
		VALUES ($1, $2, $3, $4, 'IAC-01', 'old', $5, 'new', 'unchanged', 'approved',
		        ARRAY['IAC-01.1'], $6, 'upload')
		RETURNING id`, tenant, fx.v1, bundle, oldAnchor, newVersion, newAnchor).Scan(&proposal); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	// First a system rejection, then a person's approval.
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_remap_decisions
			(tenant_id, proposal_id, decision, legacy_control_id, legacy_anchor_id,
			 legacy_scf_id, legacy_release, reason, decided_by, decided_at)
		VALUES ($1, $2, 'rejected', $3, $4, 'IAC-01', 'old', 'needs a look', 'system:scf-import', $5)`,
		tenant, proposal, fx.v1, oldAnchor, fx.rejectedAt); err != nil {
		t.Fatalf("seed rejection: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_remap_decisions
			(tenant_id, proposal_id, decision, bulk, legacy_control_id, legacy_anchor_id,
			 legacy_scf_id, legacy_release, new_control_id, new_anchor_id, new_scf_id,
			 new_release, decided_by, decided_at)
		VALUES ($1, $2, 'approved', true, $3, $4, 'IAC-01', 'old', $5, $6, 'IAC-01.1', 'new', $7, $8)`,
		tenant, proposal, fx.v1, oldAnchor, fx.v2, newAnchor, fx.approver, fx.approvedAt); err != nil {
		t.Fatalf("seed approval: %v", err)
	}
	return fx
}

// newServer serves srv's assembled router for the test's lifetime.
func newServer(t *testing.T, srv *api.Server) *httptest.Server {
	t.Helper()
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

// serve runs one GET through r.
func serve(r http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func anchorHistoryURL(id uuid.UUID, q url.Values) string {
	p := "/v1/controls/" + id.String() + "/anchor-history"
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return p
}

func TestAnchorHistory_ReturnsBundleDecisionsNewestFirst(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seedAnchorHistory(t, admin)
	env := testServer(t, app, fx.tenant)

	for _, id := range []uuid.UUID{fx.v1, fx.v2} {
		resp, body := get(t, env, anchorHistoryURL(id, nil))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET via %s: status %d body %v", id, resp.StatusCode, body)
		}
		rows, _ := body["decisions"].([]any)
		if len(rows) != 2 {
			t.Fatalf("GET via %s: %d decisions, want 2", id, len(rows))
		}
		approved, rejected := rows[0].(map[string]any), rows[1].(map[string]any)
		if approved["decision"] != "approved" || approved["bulk"] != true ||
			approved["legacy_scf_id"] != "IAC-01" || approved["legacy_release"] != "old" ||
			approved["new_scf_id"] != "IAC-01.1" || approved["new_release"] != "new" ||
			approved["new_version"] != float64(2) || approved["legacy_version"] != float64(1) ||
			approved["new_control_id"] != fx.v2.String() || approved["decided_by"] != fx.approver ||
			approved["decided_at"] != fx.approvedAt.Format(time.RFC3339Nano) {
			t.Fatalf("approval row = %v", approved)
		}
		if rejected["decision"] != "rejected" || rejected["decided_by"] != "system:scf-import" ||
			rejected["reason"] != "needs a look" || rejected["new_scf_id"] != nil || rejected["new_version"] != nil {
			t.Fatalf("rejection row = %v", rejected)
		}
	}
}

func TestAnchorHistory_KeysetPaging(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seedAnchorHistory(t, admin)
	env := testServer(t, app, fx.tenant)

	_, first := get(t, env, anchorHistoryURL(fx.v2, url.Values{"limit": {"1"}}))
	cursor, _ := first["next_cursor"].(string)
	if first["count"] != float64(1) || cursor == "" {
		t.Fatalf("page 1 = %v; want one row and a next_cursor", first)
	}
	_, second := get(t, env, anchorHistoryURL(fx.v2, url.Values{"limit": {"1"}, "cursor": {cursor}}))
	if second["count"] != float64(1) || second["next_cursor"] != "" {
		t.Fatalf("page 2 = %v; want one row and no next_cursor", second)
	}
	a := first["decisions"].([]any)[0].(map[string]any)["decision"]
	b := second["decisions"].([]any)[0].(map[string]any)["decision"]
	if a != "approved" || b != "rejected" {
		t.Fatalf("pages = %v, %v; want approved then rejected", a, b)
	}
}

func TestAnchorHistory_ViewerReadsAndOtherTenantIs404(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seedAnchorHistory(t, admin)
	other := freshTenant(t, admin)

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	viewer := testEnv{bearer: liveroletest.Bearer(t, srv, fx.tenant, "viewer")}
	foreign := testEnv{bearer: liveroletest.Bearer(t, srv, other, "admin")}
	ts := newServer(t, srv)
	viewer.server, foreign.server = ts, ts

	if resp, body := get(t, viewer, anchorHistoryURL(fx.v2, nil)); resp.StatusCode != http.StatusOK || body["count"] != float64(2) {
		t.Fatalf("viewer: status %d body %v; want 200 with both decisions", resp.StatusCode, body)
	}
	for _, id := range []uuid.UUID{fx.v1, fx.v2} {
		if resp, body := get(t, foreign, anchorHistoryURL(id, nil)); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("other tenant via %s: status %d body %v; want 404", id, resp.StatusCode, body)
		}
	}
	if resp, _ := get(t, viewer, anchorHistoryURL(uuid.New(), nil)); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown control: status %d; want 404", resp.StatusCode)
	}
}

func TestAnchorHistory_ForbidsNoRoleCredential(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	fx := seedAnchorHistory(t, admin)
	r := noRoleRouter(t, app, fx.tenant)
	rec := serve(r, anchorHistoryURL(fx.v2, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403; body %s", rec.Code, rec.Body.String())
	}
}
