//go:build integration

// #2136 — posture targets per framework version over HTTP, through the
// production middleware chain (JWT, tenancy, OPA authz). The tests pin:
//
//   - admin and grc_engineer set a target; every role reads it, on its own
//     route and on the version's GET /v1/frameworks/posture row;
//   - viewer, auditor and control_owner are refused the set (403) and leave
//     no row and no log;
//   - each set writes one audit row, before -> after, and it shows in the
//     unified audit log as kind posture_target;
//   - validation: thresholds ordered by direction, values 0..100, the
//     version visible to the tenant (404) and scoped (409).
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
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// seedPostureVersion inserts a current framework version owned by owner
// (empty for a catalog version) with one requirement, and, when scopeFor
// is set, an activated framework scope for that tenant.
func seedPostureVersion(t *testing.T, admin *pgxpool.Pool, owner, scopeFor string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	fwID, fvID := uuid.New(), uuid.New()
	var ownerArg any
	if owner != "" {
		ownerArg = owner
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("framework", `INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, $2, $3, 'Posture 2136', 'tester')`, fwID, ownerArg, "posture-2136-"+fwID.String())
	exec("framework version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, $2, $3, '2136', 'current')`, fvID, ownerArg, fwID)
	exec("requirement", `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'PT-1', 'Requirement', '')`, uuid.New(), fvID)
	scopeID := uuid.New()
	if scopeFor != "" {
		exec("framework scope", `INSERT INTO framework_scopes
				(id, tenant_id, framework_version_id, name, predicate, predicate_hash, state)
			VALUES ($1, $2, $3, 'ISMS', '{"op":"true"}'::jsonb, 'h0', 'draft')`, scopeID, scopeFor, fvID)
		exec("activate scope", `UPDATE framework_scopes SET state = 'activated', effective_from = now()
			WHERE id = $1`, scopeID)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM framework_posture_targets WHERE framework_version_id = $1`, fvID)
		_, _ = admin.Exec(ctx, `DELETE FROM framework_scopes WHERE id = $1`, scopeID)
		_, _ = admin.Exec(ctx, `DELETE FROM framework_requirements WHERE framework_version_id = $1`, fvID)
		_, _ = admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, fvID)
		_, _ = admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return fvID
}

// postureTargetLog returns the before and after of each audit row for the
// version, oldest first, as "before|after" (before "null" on the first).
func postureTargetLog(t *testing.T, admin *pgxpool.Pool, fvID uuid.UUID) []string {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT coalesce(before->>'target_value', 'null') || '|' || (after->>'target_value') || '|' || actor
		   FROM framework_posture_target_audit_log
		  WHERE framework_version_id = $1
		  ORDER BY occurred_at, id`, fvID)
	if err != nil {
		t.Fatalf("read posture target log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// postureRow finds the version's row in GET /v1/frameworks/posture.
func postureRow(t *testing.T, c periodAuditorsCall, fvID uuid.UUID) map[string]any {
	t.Helper()
	status, body := c.do(http.MethodGet, "/v1/frameworks/posture", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/frameworks/posture: %d %v", status, body)
	}
	rows, _ := body["frameworks"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m["framework_version_id"] == fvID.String() {
			return m
		}
	}
	t.Fatalf("version %s not in posture rows", fvID)
	return nil
}

func TestPostureTargets_SetReadAndRefuse(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin)
	otherTenant := dbtest.SeedTenant(t, admin)
	tid := uuid.MustParse(tenant)

	scoped := seedPostureVersion(t, admin, "", tenant)
	unscoped := seedPostureVersion(t, admin, "", "")
	foreign := seedPostureVersion(t, admin, otherTenant, otherTenant)

	roles := []string{"admin", "grc_engineer", "viewer", "auditor", "control_owner"}
	subjects := noteSubjectRoles{}
	subOf := map[string]string{}
	for _, role := range roles {
		sub := "user:" + uuid.NewString()
		subOf[role] = sub
		subjects[sub] = []authz.Role{authz.Role(role)}
	}
	engine, err := authz.NewEngine(context.Background(), subjects)
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	callers := map[string]periodAuditorsCall{}
	bearers := map[string]string{}
	for _, role := range roles {
		c := testjwt.OwnerFor(tid, []string{role})
		c.Subject = subOf[role]
		bearers[role] = srv.IssueTestJWT(t, c)
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	for _, role := range roles {
		callers[role] = periodAuditorsCall{t, ts.URL, bearers[role]}
	}

	path := func(fv uuid.UUID) string { return "/v1/framework-scopes/posture-targets/" + fv.String() }
	body := func(target, warning, critical float64, direction string) map[string]any {
		return map[string]any{
			"target_value":       target,
			"warning_threshold":  warning,
			"critical_threshold": critical,
			"direction":          direction,
		}
	}

	t.Run("no target reads 404 and the posture row carries null", func(t *testing.T) {
		if s, b := callers["viewer"].do(http.MethodGet, path(scoped), nil); s != http.StatusNotFound {
			t.Fatalf("GET before set: %d %v; want 404", s, b)
		}
		if got := postureRow(t, callers["viewer"], scoped)["target"]; got != nil {
			t.Fatalf("posture target before set = %v; want null", got)
		}
	})

	t.Run("viewer, auditor and control_owner cannot set it", func(t *testing.T) {
		for _, role := range []string{"viewer", "auditor", "control_owner"} {
			if s, b := callers[role].do(http.MethodPut, path(scoped), body(90, 80, 60, "higher_is_better")); s != http.StatusForbidden {
				t.Errorf("%s PUT: %d %v; want 403", role, s, b)
			}
		}
		if s, _ := callers["admin"].do(http.MethodGet, path(scoped), nil); s != http.StatusNotFound {
			t.Fatalf("refused sets left a target (GET %d)", s)
		}
		if got := postureTargetLog(t, admin, scoped); len(got) != 0 {
			t.Fatalf("refused sets logged %v", got)
		}
	})

	t.Run("grc_engineer sets it, every role reads it", func(t *testing.T) {
		s, b := callers["grc_engineer"].do(http.MethodPut, path(scoped), body(90, 80, 60, "higher_is_better"))
		if s != http.StatusOK {
			t.Fatalf("grc_engineer PUT: %d %v; want 200", s, b)
		}
		for _, role := range roles {
			s, got := callers[role].do(http.MethodGet, path(scoped), nil)
			if s != http.StatusOK {
				t.Fatalf("%s GET: %d %v; want 200", role, s, got)
			}
			if got["target_value"] != 90.0 || got["warning_threshold"] != 80.0 ||
				got["critical_threshold"] != 60.0 || got["direction"] != "higher_is_better" {
				t.Fatalf("%s GET = %v; want 90/80/60 higher_is_better", role, got)
			}
		}
		target, _ := postureRow(t, callers["viewer"], scoped)["target"].(map[string]any)
		if target == nil || target["target_value"] != 90.0 {
			t.Fatalf("posture row target = %v; want target_value 90", target)
		}
	})

	t.Run("admin replaces it; each set is logged before -> after", func(t *testing.T) {
		if s, b := callers["admin"].do(http.MethodPut, path(scoped), body(95, 85, 70, "higher_is_better")); s != http.StatusOK {
			t.Fatalf("admin PUT: %d %v; want 200", s, b)
		}
		want := []string{
			"null|90|" + subOf["grc_engineer"],
			"90|95|" + subOf["admin"],
		}
		got := postureTargetLog(t, admin, scoped)
		if len(got) != len(want) {
			t.Fatalf("log = %v; want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("log[%d] = %q; want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("the set shows in the unified audit log", func(t *testing.T) {
		// Read through the aggregator under the tenant (atlas_app, RLS),
		// as the unified-log routes do.
		ctx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatalf("with tenant: %v", err)
		}
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			t.Fatalf("apply tenant: %v", err)
		}
		entries, _, err := unifiedlog.Query(ctx, dbx.New(tx), unifiedlog.QueryParams{
			From:       time.Now().UTC().Add(-time.Hour),
			To:         time.Now().UTC().Add(time.Hour),
			KindFilter: []unifiedlog.Kind{unifiedlog.KindPostureTarget},
			Limit:      100,
		})
		if err != nil {
			t.Fatalf("unified log: %v", err)
		}
		n := 0
		for _, e := range entries {
			if e.TargetType == "framework_version" && e.TargetID == scoped.String() && e.Action == "set" {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("unified log posture_target rows for the version = %d; want 2 (%v)", n, entries)
		}
	})

	t.Run("validation", func(t *testing.T) {
		bad := map[string]map[string]any{
			"warning above target": body(90, 95, 60, "higher_is_better"),
			"lower reversed":       body(20, 10, 5, "lower_is_better"),
			"target band inverted": body(50, 30, 55, "target_is_better"),
			"over 100":             body(101, 80, 60, "higher_is_better"),
			"unknown direction":    body(90, 80, 60, "up"),
			"missing critical":     {"target_value": 90, "warning_threshold": 80, "direction": "higher_is_better"},
			"unknown field":        {"target_value": 90, "warning_threshold": 80, "critical_threshold": 60, "direction": "higher_is_better", "tenant_id": otherTenant},
		}
		for name, b := range bad {
			if s, got := callers["admin"].do(http.MethodPut, path(scoped), b); s != http.StatusBadRequest {
				t.Errorf("%s: %d %v; want 400", name, s, got)
			}
		}
		if s, b := callers["admin"].do(http.MethodPut, path(unscoped), body(90, 80, 60, "higher_is_better")); s != http.StatusConflict {
			t.Errorf("unscoped version: %d %v; want 409", s, b)
		}
		if s, b := callers["admin"].do(http.MethodPut, path(foreign), body(90, 80, 60, "higher_is_better")); s != http.StatusNotFound {
			t.Errorf("another tenant's version: %d %v; want 404", s, b)
		}
		if s, b := callers["admin"].do(http.MethodPut, path(uuid.New()), body(90, 80, 60, "higher_is_better")); s != http.StatusNotFound {
			t.Errorf("unknown version: %d %v; want 404", s, b)
		}
		if got := postureTargetLog(t, admin, scoped); len(got) != 2 {
			t.Fatalf("refused sets logged: %v", got)
		}
	})

	t.Run("two-decimal values with float error are accepted and stored exactly", func(t *testing.T) {
		// 70.1, 4.35 and 0.57 are not whole numbers of hundredths in
		// float64; the handler and the NUMERIC(5,2) CHECK must still agree.
		if s, b := callers["admin"].do(http.MethodPut, path(scoped), body(70.1, 4.35, 0.57, "higher_is_better")); s != http.StatusOK {
			t.Fatalf("PUT 70.1 / 4.35 / 0.57: %d %v; want 200", s, b)
		}
		if s, b := callers["admin"].do(http.MethodPut, path(scoped), body(70.1, 70.2, 70.0, "target_is_better")); s != http.StatusOK {
			t.Fatalf("PUT equal target band: %d %v; want 200", s, b)
		}
		s, got := callers["viewer"].do(http.MethodGet, path(scoped), nil)
		if s != http.StatusOK || got["target_value"] != 70.1 || got["warning_threshold"] != 70.2 || got["critical_threshold"] != 70.0 {
			t.Fatalf("GET = %d %v; want 70.1 / 70.2 / 70", s, got)
		}
	})

	t.Run("another tenant does not see the target", func(t *testing.T) {
		c := testjwt.OwnerFor(uuid.MustParse(otherTenant), []string{"admin"})
		c.Subject = subOf["admin"]
		other := periodAuditorsCall{t, ts.URL, srv.IssueTestJWT(t, c)}
		if s, b := other.do(http.MethodGet, path(scoped), nil); s != http.StatusNotFound {
			t.Fatalf("other tenant GET: %d %v; want 404", s, b)
		}
	})
}
