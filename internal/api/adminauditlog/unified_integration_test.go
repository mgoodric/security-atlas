//go:build integration

// Integration tests for the slice 124 unified audit-log aggregation endpoint.
// Requires Postgres reachable via DATABASE_URL_APP.
//
// The suite seeds rows in ALL NINE per-domain audit-log tables under two
// tenants, then queries the unified endpoint under each tenant's context and
// asserts the RLS contract: each tenant sees ONLY their own rows across all
// nine kinds (slice 124 AC-9). A separate pagination test seeds 1500 rows
// across three tables for one tenant and walks the cursor.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

// newUnifiedRouter wires the slice-124 endpoint under the same auth + tenancy
// middleware stack the production server uses. isAdmin maps to the credential
// IsAdmin flag; the handler also probes user_roles for auditor / grc_engineer
// (not exercised here — the IsAdmin short-circuit covers the happy path).
func newUnifiedRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool) http.Handler {
	t.Helper()
	h := adminauditlog.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_unified",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "user-unified-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/audit-log/unified", h.UnifiedList)
	return r
}

// seedUnifiedRow inserts ONE row into the named audit-log table under tenant's
// GUC. The nine tables have heterogeneous shapes; this helper centralises the
// per-table minimum-INSERT shape so the test body stays focused on assertions.
// Returns the row's canonical occurred_at.
func seedUnifiedRow(t *testing.T, tenantID uuid.UUID, table string) time.Time {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed set_config: %v", err)
	}

	var ts time.Time
	var q string
	var args []any

	switch table {
	case "decision_audit_log":
		q = `INSERT INTO decision_audit_log
		     (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
		     VALUES (gen_random_uuid(), $1, 'seeder', 'list', 'evidence', 'r-1', 'allow')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "evidence_audit_log":
		q = `INSERT INTO evidence_audit_log
		     (id, tenant_id, credential_id, decision)
		     VALUES (gen_random_uuid(), $1, 'key_seed', 'accepted')
		     RETURNING received_at`
		args = []any{tenantID}
	case "exception_audit_log":
		q = `INSERT INTO exception_audit_log
		     (id, tenant_id, exception_id, action, actor, to_state)
		     VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'requested', 'seeder', 'requested')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "sample_audit_log":
		q = `INSERT INTO sample_audit_log
		     (id, tenant_id, action, actor)
		     VALUES (gen_random_uuid(), $1, 'sample_drawn', 'seeder')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "audit_period_audit_log":
		q = `INSERT INTO audit_period_audit_log
		     (id, tenant_id, audit_period_id, action, actor)
		     VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'period_created', 'seeder')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "feature_flag_audit_log":
		q = `INSERT INTO feature_flag_audit_log
		     (id, tenant_id, flag_key, from_enabled, to_enabled, actor)
		     VALUES (gen_random_uuid(), $1, 'risk.enabled', true, false, 'seeder')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "me_audit_log":
		q = `INSERT INTO me_audit_log
		     (tenant_id, user_id, action)
		     VALUES ($1, gen_random_uuid(), 'profile.update')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "walkthrough_audit_log":
		q = `INSERT INTO walkthrough_audit_log
		     (id, tenant_id, walkthrough_id, action, actor)
		     VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'walkthrough_created', 'seeder')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "aggregation_rule_audit_log":
		// Needs a parent aggregation_rules row.
		ruleID := uuid.New()
		if _, err := tx.Exec(ctx,
			`INSERT INTO aggregation_rules (
			    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
			    window_days, parent_level, severity_function, rule_body
			 ) VALUES (
			    $1, $2, $3, 'ownership', 3, 2, 30, 'team', 'max', '{}'::jsonb
			 )`,
			ruleID, tenantID, "rule-"+ruleID.String()[:8],
		); err != nil {
			t.Fatalf("seed aggregation_rules: %v", err)
		}
		q = `INSERT INTO aggregation_rule_audit_log
		     (id, tenant_id, rule_id, event, actor)
		     VALUES (gen_random_uuid(), $1, $2, 'created', 'seeder')
		     RETURNING created_at`
		args = []any{tenantID, ruleID}
	default:
		t.Fatalf("seedUnifiedRow: unsupported table %q", table)
	}

	if err := tx.QueryRow(ctx, q, args...).Scan(&ts); err != nil {
		t.Fatalf("seed %s: %v", table, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	return ts
}

// cleanupUnifiedTables purges seeded rows for the given tenant across every
// audit-log table the unified endpoint reads. Runs at test cleanup so each
// test owns a clean slate.
func cleanupUnifiedTables(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		// Order matters: aggregation_rule_audit_log has FK to aggregation_rules,
		// so delete the audit rows first, then the parent rules.
		for _, tbl := range []string{
			"decision_audit_log",
			"evidence_audit_log",
			"exception_audit_log",
			"sample_audit_log",
			"audit_period_audit_log",
			"feature_flag_audit_log",
			"me_audit_log",
			"walkthrough_audit_log",
			"aggregation_rule_audit_log",
			"aggregation_rules",
		} {
			_, _ = appPool.Exec(ctx,
				fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", tbl), tenantID)
		}
	})
}

// allNineKinds is the canonical wire mapping from kind to underlying table.
// Stays adjacent to the test that depends on it so a kind addition trips the
// test count assertion immediately.
var allNineKinds = []struct {
	table string
	kind  string
}{
	{"decision_audit_log", "decision"},
	{"evidence_audit_log", "evidence"},
	{"exception_audit_log", "exception"},
	{"sample_audit_log", "sample"},
	{"audit_period_audit_log", "audit_period"},
	{"feature_flag_audit_log", "feature_flag"},
	{"me_audit_log", "me"},
	{"walkthrough_audit_log", "walkthrough"},
	{"aggregation_rule_audit_log", "aggregation_rule"},
}

// AC-9: per-tenant isolation across all nine audit-log tables.
//
// Tenant A and Tenant B each seed exactly one row in each of the nine
// underlying tables. A query as Tenant A returns exactly nine rows — one per
// kind — all carrying tenant_id = A. A query as Tenant B returns the same
// nine kinds, all carrying tenant_id = B. RLS on each base table is the
// load-bearing contract; this test fails fast if any branch of the UNION ALL
// bypasses it.
func TestSlice124_UnifiedTenantIsolationAcrossAllNineKinds(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	cleanupUnifiedTables(t, tenantA)
	cleanupUnifiedTables(t, tenantB)

	for _, kc := range allNineKinds {
		seedUnifiedRow(t, tenantA, kc.table)
		seedUnifiedRow(t, tenantB, kc.table)
	}

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	for _, who := range []struct {
		name   string
		tenant uuid.UUID
	}{
		{"tenant_A", tenantA},
		{"tenant_B", tenantB},
	} {
		who := who
		t.Run(who.name, func(t *testing.T) {
			r := newUnifiedRouter(t, who.tenant, true)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
			}
			var resp adminauditlog.UnifiedListResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			seenKinds := map[string]int{}
			for _, e := range resp.Entries {
				seenKinds[e.Kind]++
				if e.TenantID != who.tenant {
					t.Errorf("RLS leak: entry tenant_id = %s; want %s; kind=%s",
						e.TenantID, who.tenant, e.Kind)
				}
			}
			for _, kc := range allNineKinds {
				if seenKinds[kc.kind] != 1 {
					t.Errorf("kind %q count = %d; want exactly 1 (seeded one row for this tenant)",
						kc.kind, seenKinds[kc.kind])
				}
			}
			if len(resp.Entries) != len(allNineKinds) {
				t.Errorf("total entries = %d; want %d (one per kind, RLS-filtered to this tenant)",
					len(resp.Entries), len(allNineKinds))
			}
		})
	}
}

// AC-10: every successful query writes a me_audit_log row with
// action='audit_log_query_unified' under the caller's tenant.
func TestSlice124_MetaAuditWrittenOnEveryQuery(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	// Seed one row so the response is non-empty (the meta-audit fires regardless
	// of result count, but a populated response makes the after-blob assertion
	// meaningful).
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	r := newUnifiedRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first query status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Count meta-audit rows under the tenant's GUC.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe tx: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("probe set_config: %v", err)
	}
	var nFirst int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'audit_log_query_unified'`,
		tenant,
	).Scan(&nFirst); err != nil {
		t.Fatalf("probe count: %v", err)
	}
	if nFirst != 1 {
		t.Errorf("after first query: me_audit_log rows = %d; want 1", nFirst)
	}

	// A second query writes a second meta-audit row — the meta-audit fires
	// per call, not per distinct request shape (load-bearing for slice 124's
	// AC-10 — every query is auditable).
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("second query status = %d", rec.Code)
	}
	var nSecond int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'audit_log_query_unified'`,
		tenant,
	).Scan(&nSecond); err != nil {
		t.Fatalf("probe count 2: %v", err)
	}
	if nSecond != 2 {
		t.Errorf("after second query: me_audit_log rows = %d; want 2", nSecond)
	}
}

// AC-11: cursor pagination walks the result without duplicates and surfaces
// `next_cursor` until the last page.
//
// Seeds 1500 rows for one tenant across three tables (500 each) and pages
// through. Each page MUST be ordered occurred_at DESC, and no entry must
// appear on more than one page.
func TestSlice124_CursorPaginationWalksAllRows(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	const totalPerTable = 500
	const tablesUsed = 3
	const wantTotal = totalPerTable * tablesUsed // 1500
	const pageSize = 1000

	// Bulk seed via direct INSERT … SELECT generate_series to keep the test
	// runtime sane. Three tables chosen for shape diversity (decision uses
	// occurred_at + user_id; evidence uses received_at + credential_id; me
	// uses occurred_at + user_id::text via the UUID cast).
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin bulk seed: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("bulk seed set_config: %v", err)
	}

	// Use distinct occurred_at values per row so the ORDER BY is unambiguous
	// even at page boundaries (no clock-tie ambiguity).
	bulkInserts := []string{
		`INSERT INTO decision_audit_log
		 (decision_id, tenant_id, user_id, action, resource_type, resource_id, result, occurred_at)
		 SELECT gen_random_uuid(), $1, 'seeder', 'list', 'evidence', 'r-' || i, 'allow',
		        now() - (i || ' microseconds')::interval
		 FROM generate_series(1, $2) AS i`,
		`INSERT INTO evidence_audit_log
		 (id, tenant_id, credential_id, decision, received_at)
		 SELECT gen_random_uuid(), $1, 'key_seed', 'accepted',
		        now() - (($2 + i) || ' microseconds')::interval
		 FROM generate_series(1, $2) AS i`,
		`INSERT INTO me_audit_log
		 (tenant_id, user_id, action, occurred_at)
		 SELECT $1, gen_random_uuid(), 'profile.update',
		        now() - ((2 * $2 + i) || ' microseconds')::interval
		 FROM generate_series(1, $2) AS i`,
	}
	for _, ins := range bulkInserts {
		if _, err := tx.Exec(ctx, ins, tenant, totalPerTable); err != nil {
			t.Fatalf("bulk seed: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("bulk seed commit: %v", err)
	}

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)

	r := newUnifiedRouter(t, tenant, true)

	seen := map[string]bool{} // target_id+kind -> seen
	var (
		page       int
		nextCursor string
		totalSeen  int
		lastTS     *time.Time
	)
	for {
		page++
		if page > 5 {
			t.Fatalf("paginator did not terminate; got %d pages", page)
		}
		url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)
		if nextCursor != "" {
			url += "&cursor=" + nextCursor
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d status = %d; body = %s", page, rec.Code, rec.Body.String())
		}
		var resp adminauditlog.UnifiedListResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode page %d: %v", page, err)
		}

		for _, e := range resp.Entries {
			// Use RowID (the audit-row's own UUID PK projected via the
			// row_id column of the UNION) as the dedup key. RowID is
			// guaranteed unique per row across the union — using
			// (kind, target_id) would collapse legitimately-distinct
			// rows whose target identifier is empty or shared.
			key := e.RowID.String()
			if seen[key] {
				t.Errorf("duplicate entry across pages: kind=%s row_id=%s",
					e.Kind, e.RowID)
			}
			seen[key] = true
			if lastTS != nil && e.OccurredAt.After(*lastTS) {
				t.Errorf("ordering violation: page %d has out-of-order ts %v after %v",
					page, e.OccurredAt, *lastTS)
			}
			ts := e.OccurredAt
			lastTS = &ts
		}
		totalSeen += len(resp.Entries)

		switch page {
		case 1:
			if len(resp.Entries) != pageSize {
				t.Errorf("page 1 entries = %d; want %d", len(resp.Entries), pageSize)
			}
			if resp.NextCursor == "" {
				t.Errorf("page 1 next_cursor empty; want non-empty (more rows expected)")
			}
		case 2:
			if got, want := len(resp.Entries), wantTotal-pageSize; got != want {
				t.Errorf("page 2 entries = %d; want %d", got, want)
			}
			if resp.NextCursor != "" {
				t.Errorf("page 2 next_cursor = %q; want empty (no more rows)", resp.NextCursor)
			}
		}

		nextCursor = resp.NextCursor
		if nextCursor == "" {
			break
		}
	}

	if totalSeen != wantTotal {
		t.Errorf("total entries paged = %d; want %d", totalSeen, wantTotal)
	}
}

// Window guard: a request whose `to - from > 90 days` returns 400 before
// touching the DB (slice 124 AC-5).
func TestSlice124_RejectsWindowOver90Days(t *testing.T) {
	tenant := uuid.New()
	r := newUnifiedRouter(t, tenant, true)

	from := time.Now().Add(-120 * 24 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want %d (400 — window > 90 days)",
			rec.Code, http.StatusBadRequest)
	}
}

// Missing from/to → 400 before any DB query (slice 124 AC-5).
func TestSlice124_RejectsMissingRequiredParams(t *testing.T) {
	tenant := uuid.New()
	r := newUnifiedRouter(t, tenant, true)

	cases := []string{
		"/v1/admin/audit-log/unified",
		"/v1/admin/audit-log/unified?from=2026-05-01T00:00:00Z",
		"/v1/admin/audit-log/unified?to=2026-05-10T00:00:00Z",
	}
	for _, url := range cases {
		url := url
		t.Run(url, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d; want %d (400 — missing required param)",
					rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// Non-admin without user_roles row → 403 from the defense-in-depth check.
func TestSlice124_RejectsCallerWithoutEligibleRole(t *testing.T) {
	tenant := uuid.New()
	r := newUnifiedRouter(t, tenant, false) // not admin; no user_roles seeded

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want %d (403 — caller is neither admin nor auditor/grc_engineer)",
			rec.Code, http.StatusForbidden)
	}
}

// ===== Slice 129 — actor_name LEFT JOIN extension =====

// seedTenantUser inserts ONE row into `users` under tenant's GUC and returns
// the user's id. The caller controls the id so the cross-tenant collision
// test (`TestSlice129_ActorNameRLSIsolatedAcrossTenants`) can deliberately
// seed the SAME UUID under both tenants and prove RLS keeps the JOIN clean.
func seedTenantUser(t *testing.T, tenantID, userID uuid.UUID, email, displayName string) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed user begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed user set_config: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name) VALUES ($1, $2, $3, $4)`,
		userID, tenantID, email, displayName,
	); err != nil {
		t.Fatalf("seed user INSERT: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed user commit: %v", err)
	}
	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(),
			`DELETE FROM users WHERE id = $1 AND tenant_id = $2`, userID, tenantID)
	})
}

// seedMeAuditRowForUser inserts ONE me_audit_log row carrying user_id (which
// becomes the actor_id projection in the unified read). Returns the row's
// occurred_at. Cleanup happens via cleanupUnifiedTables when the caller pairs
// this with that helper.
func seedMeAuditRowForUser(t *testing.T, tenantID, userID uuid.UUID) time.Time {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed me-audit begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed me-audit set_config: %v", err)
	}
	var ts time.Time
	if err := tx.QueryRow(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action) VALUES ($1, $2, 'profile.update') RETURNING occurred_at`,
		tenantID, userID,
	).Scan(&ts); err != nil {
		t.Fatalf("seed me-audit INSERT: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed me-audit commit: %v", err)
	}
	return ts
}

// AC-3 (slice 129): an actor_id that resolves to a users row in the same
// tenant carries the display_name in the response. The me_audit_log kind
// projects user_id::text as actor_id, which matches the UUID regex and
// joins cleanly.
func TestSlice129_ActorNameResolvedWhenUserRowExists(t *testing.T) {
	tenant := uuid.New()
	user := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedTenantUser(t, tenant, user, "alice@test-example.test", "Alice Test")
	seedMeAuditRowForUser(t, tenant, user)

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	r := newUnifiedRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Find the me-kind row keyed to our seeded user_id and assert
	// actor_name resolves.
	var found *adminauditlog.UnifiedEntry
	for i := range resp.Entries {
		e := &resp.Entries[i]
		if e.Kind == "me" && e.ActorID == user.String() {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatalf("seeded me_audit_log row not present in response; got %d entries", len(resp.Entries))
	}
	if found.ActorName == nil {
		t.Fatalf("actor_name = nil; want \"Alice Test\" (users row exists for this actor_id)")
	}
	if *found.ActorName != "Alice Test" {
		t.Errorf("actor_name = %q; want %q", *found.ActorName, "Alice Test")
	}

	// (The slice-124 meta-audit row for THIS query is written AFTER the
	// aggregator read in the same transaction, so a single-query test
	// cannot observe it in its own response. The AC-4
	// `TestSlice129_ActorNameNilWhenNoUserRow` test below covers the
	// no-users-row → actor_name=nil branch.)
}

// AC-4 (slice 129): a row whose actor_id has NO matching users row returns
// actor_name=nil rather than failing. The decision-kind seed uses a literal
// `'seeder'` for user_id — non-UUID — so the JOIN's regex rejects the cast
// and the LEFT JOIN harmlessly yields no match.
func TestSlice129_ActorNameNilWhenNoUserRow(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	// decision_audit_log's seed uses user_id='seeder' (string literal,
	// non-UUID). The JOIN regex rejects it and the row's actor_name
	// remains nil.
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	r := newUnifiedRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Capture the raw body BEFORE decoding so the AC-4 wire-shape check
	// below can inspect the literal JSON. json.NewDecoder consumes the
	// underlying *bytes.Buffer.
	rawBody := rec.Body.String()

	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(strings.NewReader(rawBody)).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var decision *adminauditlog.UnifiedEntry
	for i := range resp.Entries {
		if resp.Entries[i].Kind == "decision" {
			decision = &resp.Entries[i]
			break
		}
	}
	if decision == nil {
		t.Fatalf("decision-kind row missing from response")
	}
	if decision.ActorID != "seeder" {
		t.Fatalf("seeded actor_id = %q; want %q (seedUnifiedRow contract)", decision.ActorID, "seeder")
	}
	if decision.ActorName != nil {
		t.Errorf("actor_name = %q; want nil (no users row matches non-UUID actor_id)",
			*decision.ActorName)
	}

	// AC-4 explicit wire-shape check: the JSON serialization MUST carry
	// actor_name as JSON `null` (not absent, not empty string). This
	// guards against an accidental `omitempty` regression that would
	// hide the field on null callers.
	if !strings.Contains(rawBody, `"actor_name":null`) {
		t.Errorf("response body MUST carry \"actor_name\":null for unresolved actors; got: %s", rawBody)
	}
}

// P0-A1 (slice 129): tenant isolation across the JOIN.
//
// Threat model row I (information disclosure): a LEFT JOIN onto `users`
// could leak Tenant B's display names into Tenant A's audit-log response
// if RLS on `users` were bypassed or if the JOIN itself were not
// tenant-scoped.
//
// Test design: seed a user row ONLY in Tenant B (display="leaked-secret").
// Then seed an audit row in Tenant A keyed to THE SAME UUID as Tenant B's
// user. Tenant A's query MUST return actor_name=nil for that audit row,
// because:
//
//   - RLS on `users` makes Tenant B's user invisible from Tenant A's
//     session (the load-bearing contract).
//   - The explicit `ON u.tenant_id = unified.tenant_id` predicate is
//     defense-in-depth on top of RLS.
//
// If actor_name comes back as "leaked-secret", at least one of those
// barriers has broken and the slice is a P0 cross-tenant info-disclosure
// regression.
//
// (Note: `users.id` is a global PRIMARY KEY, so we cannot seed the same
// UUID under both tenants simultaneously — the leak vector is "one
// tenant's user UUID happens to appear as an actor_id in a different
// tenant's audit row", which this test exercises directly.)
func TestSlice129_ActorNameRLSIsolatedAcrossTenants(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	tenantBUserID := uuid.New() // exists only in Tenant B

	cleanupUnifiedTables(t, tenantA)
	cleanupUnifiedTables(t, tenantB)
	seedTenantUser(t, tenantB, tenantBUserID, "leaked@test-tenant-b.test", "leaked-secret")

	// Audit row in Tenant A whose actor_id is Tenant B's user UUID.
	// If the JOIN were not tenant-scoped (or if RLS on users were
	// bypassed) Tenant A's response would carry "leaked-secret" as
	// actor_name. That MUST NOT happen.
	seedMeAuditRowForUser(t, tenantA, tenantBUserID)

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to)

	rA := newUnifiedRouter(t, tenantA, true)
	recA := httptest.NewRecorder()
	rA.ServeHTTP(recA, httptest.NewRequest(http.MethodGet, url, nil))
	if recA.Code != http.StatusOK {
		t.Fatalf("tenant A status = %d; body = %s", recA.Code, recA.Body.String())
	}
	var respA adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(recA.Body).Decode(&respA); err != nil {
		t.Fatalf("decode A: %v", err)
	}
	var foundA *adminauditlog.UnifiedEntry
	for i := range respA.Entries {
		e := &respA.Entries[i]
		if e.Kind == "me" && e.ActorID == tenantBUserID.String() {
			foundA = e
			break
		}
	}
	if foundA == nil {
		t.Fatalf("tenant A: seeded me_audit_log row missing from response (got %d entries)",
			len(respA.Entries))
	}
	if foundA.ActorName != nil {
		t.Errorf("CROSS-TENANT LEAK: tenant A's response carries actor_name=%q; want nil "+
			"(the users row lives in tenant B only — RLS or the tenant-scoped JOIN MUST "+
			"keep it hidden)",
			*foundA.ActorName)
	}

	// Sanity check: as Tenant B, query for that user's id and confirm the
	// users row IS visible (so the absence in Tenant A is a tenant-scope
	// effect, not a missing-row artifact). We don't have an audit row in
	// Tenant B (it's only in A), so the unified query returns no row for
	// that actor_id; instead, probe users directly under Tenant B's GUC.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("tenant B probe begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantB.String()); err != nil {
		t.Fatalf("tenant B probe set_config: %v", err)
	}
	var displayName string
	if err := tx.QueryRow(ctx,
		`SELECT display_name FROM users WHERE id = $1`, tenantBUserID,
	).Scan(&displayName); err != nil {
		t.Fatalf("tenant B probe: should see own users row; got error: %v", err)
	}
	if displayName != "leaked-secret" {
		t.Errorf("tenant B probe: display_name = %q; want %q (control: own-tenant read should work)",
			displayName, "leaked-secret")
	}
}
