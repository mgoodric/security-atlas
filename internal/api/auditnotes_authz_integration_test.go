//go:build integration

// #2193 — an auditor writes audit notes over HTTP. The auditor.rego write rule
// compares input.resource.attrs.audit_period_id against the auditor's
// assigned periods, so the authz middleware must hydrate that attribute from
// the request body (after checking the period exists in the caller's tenant)
// before the decision. These tests drive POST /v1/audit-notes through the
// production middleware chain with a "user:"-prefixed subject, the form a
// local-login / PKCE / device-grant JWT carries.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// noteSubjectRoles is a stub authz.RolesResolver keyed on the raw subject,
// the same raw value the engine's roles hop receives in production.
type noteSubjectRoles map[string][]authz.Role

func (m noteSubjectRoles) RolesFor(_ context.Context, _, userID string) ([]authz.Role, error) {
	return m[userID], nil
}

// seedNotePeriod inserts a framework version and an open audit period in
// tenant, removing both when the test ends.
func seedNotePeriod(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	fwID, versionID, periodID := uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx,
		`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, '#2193 test framework', $2, 'test')`,
		fwID, "test-2193-"+fwID.String()); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx,
		`INSERT INTO framework_versions (id, tenant_id, framework_id, version) VALUES ($1, NULL, $2, '1.0')`,
		versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end, status, created_by)
		VALUES ($1, $2, $4, $3, '2026-01-01', '2026-03-31', 'open', 'test-seed')`,
		periodID, tenant, versionID, "#2193 period "+periodID.String()); err != nil {
		t.Fatalf("seed audit_period: %v", err)
	}
	t.Cleanup(func() {
		// Runs before SeedTenant's cleanup (LIFO), so notes and assignments
		// go first, then the period, then the global framework rows.
		for _, stmt := range []string{
			`DELETE FROM notifications WHERE tenant_id = $1`,
			`DELETE FROM audit_notes WHERE tenant_id = $1`,
			`DELETE FROM auditor_assignments WHERE tenant_id = $1`,
			`DELETE FROM decision_audit_log WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %q: %v", stmt, err)
			}
		}
		_, _ = admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, versionID)
		_, _ = admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return periodID
}

func postAuditNote(t *testing.T, url, bearer string, periodID uuid.UUID) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"audit_period_id": periodID.String(),
		"scope_type":      "period",
		"scope_id":        periodID.String(),
		"body":            "sampled 25 tickets; 2 missing approval",
		"visibility":      "auditor_only",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"/v1/audit-notes", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/audit-notes: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func TestAuditNotes_AuditorWritesOnlyInAssignedPeriods(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin)
	otherTenant := dbtest.SeedTenant(t, admin)
	tid := uuid.MustParse(tenant)

	assigned := seedNotePeriod(t, admin, tenant)
	unassigned := seedNotePeriod(t, admin, tenant)
	foreign := seedNotePeriod(t, admin, otherTenant)

	assignedUser, unassignedUser := uuid.New(), uuid.New()
	store := auditor.NewStore(app)
	// Assignments are keyed by the bare users(id).
	if err := store.Assign(dbtest.WithTenantCtx(t, tenant), assignedUser.String(), assigned, "test"); err != nil {
		t.Fatalf("assign auditor: %v", err)
	}
	// The same person is also an auditor of a period in another tenant.
	// Signed in to tenant, they still cannot write into that period.
	if err := store.Assign(dbtest.WithTenantCtx(t, otherTenant), assignedUser.String(), foreign, "test"); err != nil {
		t.Fatalf("assign auditor in other tenant: %v", err)
	}
	// The unassigned auditor holds a grant, just not for this period.
	if err := store.Assign(dbtest.WithTenantCtx(t, tenant), unassignedUser.String(), unassigned, "test"); err != nil {
		t.Fatalf("assign second auditor: %v", err)
	}

	assignedClaims := testjwt.OwnerFor(tid, []string{"auditor"})
	assignedClaims.Subject = "user:" + assignedUser.String()
	unassignedClaims := testjwt.OwnerFor(tid, []string{"auditor"})
	unassignedClaims.Subject = "user:" + unassignedUser.String()

	engine, err := authz.NewEngine(context.Background(), noteSubjectRoles{
		assignedClaims.Subject:   {authz.RoleAuditor},
		unassignedClaims.Subject: {authz.RoleAuditor},
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	engine = engine.WithAttrsResolver(auditor.NewDBAttrsResolver(app))

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	assignedBearer := srv.IssueTestJWT(t, assignedClaims)
	unassignedBearer := srv.IssueTestJWT(t, unassignedClaims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	cases := []struct {
		name   string
		bearer string
		period uuid.UUID
		want   int
	}{
		{"assigned auditor, assigned period", assignedBearer, assigned, http.StatusCreated},
		{"assigned auditor, another period in the tenant", assignedBearer, unassigned, http.StatusForbidden},
		{"assigned auditor, their period in another tenant", assignedBearer, foreign, http.StatusForbidden},
		{"assigned auditor, period that does not exist", assignedBearer, uuid.New(), http.StatusForbidden},
		{"unassigned auditor, period they are not assigned to", unassignedBearer, assigned, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := postAuditNote(t, ts.URL, tc.bearer, tc.period)
			if status != tc.want {
				t.Fatalf("POST /v1/audit-notes: status %d body=%s; want %d", status, body, tc.want)
			}
		})
	}
}
