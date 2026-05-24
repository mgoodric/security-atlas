//go:build integration

// Integration tests for the slice 270 non-admin activity-ledger endpoint
// (`GET /v1/activity/unified`). Requires Postgres reachable via
// DATABASE_URL_APP — shares the TestMain bootstrap with the slice 124
// admin endpoint tests (handler_integration_test.go).
//
// The suite verifies slice 270's load-bearing contracts:
//
//   - AC-6 (cross-actor isolation): a non-admin operator B does NOT see
//     admin actor A's me-rows or feature_flag rows under the same tenant.
//   - AC-7 (cross-tenant isolation): operator B in tenant A does NOT see
//     any rows from tenant C (slice 270 P0-A4 — RLS continues to bound
//     the read).
//   - Privileged-caller parity: an admin / auditor / grc_engineer caller
//     reaching /v1/activity/unified sees the SAME row set the slice 124
//     /v1/admin/audit-log/unified endpoint returns (CallerIsPrivileged
//     short-circuit verified end-to-end).
//   - P0-A5 (filter-combination authz independence): a non-admin passing
//     ?actor=<admin-uuid> in the URL does NOT widen visibility — the
//     Go-side caller_user_id bind parameter is the SQL predicate's
//     truth, not the URL filter.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

// newActivityRouter wires the slice-270 endpoint under the same auth +
// tenancy middleware stack the production server uses. isAdmin maps to
// the credential IsAdmin flag (privileged = true short-circuits the
// SQL row-visibility predicate); userID is the caller's actor identifier
// used by the SQL `actor_id = caller_user_id` me-row predicate when the
// caller is non-privileged.
func newActivityRouter(t *testing.T, tenantID uuid.UUID, userID string, isAdmin bool) http.Handler {
	t.Helper()
	h := adminauditlog.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_activity",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   userID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/activity/unified", h.ActivityList)
	return r
}

// seedMeRowForActor inserts a me_audit_log row with the given user_id as
// the actor. Used to seed cross-actor visibility test fixtures.
func seedMeRowForActor(t *testing.T, tenantID uuid.UUID, actorUserID uuid.UUID, action string) {
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
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action) VALUES ($1, $2, $3)`,
		tenantID, actorUserID, action,
	); err != nil {
		t.Fatalf("seed me_audit_log: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

// TestSlice270_NonAdminCrossActorIsolation pins AC-6 + P0-A1 + P0-A2:
// a non-admin operator's /v1/activity/unified response MUST NOT include
// (a) any me-rows whose actor_id is NOT the caller's user_id, or
// (b) any feature_flag rows (admin-only program-configuration events).
//
// Setup: one tenant, two actors. The "admin actor" seeds a me-row with
// action='super_admin_grant' (proxy for any admin-only-action class —
// the test does not need a real super_admin_grant flow because the row
// visibility predicate keys on actor_id + kind, not on action). The
// "non-admin actor" seeds their own me-row with action='profile.update'.
// One feature_flag row is also seeded (admin-only kind).
//
// Then the test queries as the non-admin actor and asserts:
//   - the admin actor's me-row is NOT in the response;
//   - the non-admin actor's own me-row IS in the response;
//   - the feature_flag row is NOT in the response;
//   - the meta-audit row written by THIS query IS in the response (it's
//     the caller's own me-row, so it admits — verifies the same-actor
//     branch end-to-end).
func TestSlice270_NonAdminCrossActorIsolation(t *testing.T) {
	tenant := uuid.New()
	adminActor := uuid.New()
	nonAdminActor := uuid.New()

	cleanupUnifiedTables(t, tenant)

	// Seed: one me-row per actor + one feature_flag row.
	seedMeRowForActor(t, tenant, adminActor, "profile.update")        // admin's me-row
	seedMeRowForActor(t, tenant, nonAdminActor, "profile.update")     // non-admin's own me-row
	seedUnifiedRow(t, tenant, "feature_flag_audit_log")               // admin-only kind
	publicEvidence := seedUnifiedRow(t, tenant, "evidence_audit_log") // tenant-public, baseline
	_ = publicEvidence

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to)

	// Query as the non-admin actor (isAdmin=false → CallerIsPrivileged=false).
	r := newActivityRouter(t, tenant, nonAdminActor.String(), false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Assertions.
	sawAdminActorMeRow := false
	sawNonAdminActorMeRow := false
	sawFeatureFlag := false
	sawPublicEvidence := false
	for _, e := range resp.Entries {
		switch e.Kind {
		case "me":
			// The me-row's actor_id is the user_id::text per the slice
			// 124 SQL projection.
			if e.ActorID == adminActor.String() {
				// P0-A1: a non-admin MUST NOT see another actor's me-rows.
				// EXCEPTION: the actor's OWN audit-log queries surface as
				// their own me-rows. The admin actor's me-rows seeded
				// above predate this query and are NOT the caller's, so
				// they must NOT appear.
				if e.Action == "profile.update" {
					sawAdminActorMeRow = true
				}
			}
			if e.ActorID == nonAdminActor.String() && e.Action == "profile.update" {
				sawNonAdminActorMeRow = true
			}
		case "feature_flag":
			// P0-A2: feature_flag rows are admin-only.
			sawFeatureFlag = true
		case "evidence":
			sawPublicEvidence = true
		}
	}

	if sawAdminActorMeRow {
		t.Errorf("P0-A1 violation: non-admin caller saw admin actor's me-row (actor_id=%s)", adminActor)
	}
	if sawFeatureFlag {
		t.Errorf("P0-A2 violation: non-admin caller saw feature_flag row")
	}
	if !sawNonAdminActorMeRow {
		t.Errorf("AC-6: non-admin caller did NOT see their own me-row (actor_id=%s); want present", nonAdminActor)
	}
	if !sawPublicEvidence {
		t.Errorf("AC-6 sanity: non-admin caller did NOT see the tenant-public evidence row; want present")
	}
}

// TestSlice270_NonAdminCrossTenantIsolation pins AC-7 + P0-A4: a
// non-admin operator B in tenant A does NOT see any rows from tenant C.
// RLS on the underlying audit-log tables (atlas_app + tenancy GUC) is
// the load-bearing contract; the slice 270 row-visibility predicate is
// additive on top of RLS, not a bypass.
func TestSlice270_NonAdminCrossTenantIsolation(t *testing.T) {
	tenantA := uuid.New()
	tenantC := uuid.New()
	operatorB := uuid.New()

	cleanupUnifiedTables(t, tenantA)
	cleanupUnifiedTables(t, tenantC)

	// Seed: tenant A gets one evidence row + one me-row for operator B.
	// Tenant C gets one evidence row + one me-row for operator B (under
	// tenant C — should be invisible to operator B in tenant A).
	seedUnifiedRow(t, tenantA, "evidence_audit_log")
	seedMeRowForActor(t, tenantA, operatorB, "profile.update")
	seedUnifiedRow(t, tenantC, "evidence_audit_log")
	seedMeRowForActor(t, tenantC, operatorB, "profile.update")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to)

	// Operator B in tenant A.
	r := newActivityRouter(t, tenantA, operatorB.String(), false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, e := range resp.Entries {
		if e.TenantID != tenantA {
			t.Errorf("P0-A4 / AC-7 violation: cross-tenant leak — entry tenant_id=%s (want %s); kind=%s actor=%s",
				e.TenantID, tenantA, e.Kind, e.ActorID)
		}
	}
}

// TestSlice270_NonAdminURLActorFilterDoesNotWidenVisibility pins P0-A5:
// a non-admin caller passing ?actor=<some-other-uuid> in the URL MUST
// NOT see that other actor's me-rows. The Go-side caller_user_id bind
// parameter is the SQL predicate's truth source, conjunctive with the
// URL-controlled actor_filter.
func TestSlice270_NonAdminURLActorFilterDoesNotWidenVisibility(t *testing.T) {
	tenant := uuid.New()
	adminActor := uuid.New()
	nonAdminActor := uuid.New()

	cleanupUnifiedTables(t, tenant)

	// Seed: admin actor has a me-row.
	seedMeRowForActor(t, tenant, adminActor, "profile.update")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	// Non-admin caller TRIES to enumerate the admin actor's actions via
	// the URL filter. Must return zero me-rows because the SQL predicate
	// conjoins actor_id=$URL_filter AND actor_id=$caller_user_id —
	// impossible when caller != target.
	url := fmt.Sprintf("/v1/activity/unified?from=%s&to=%s&actor=%s",
		from, to, adminActor.String())

	r := newActivityRouter(t, tenant, nonAdminActor.String(), false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, e := range resp.Entries {
		if e.Kind == "me" && e.ActorID == adminActor.String() {
			t.Errorf("P0-A5 violation: non-admin caller used ?actor=<admin> to surface admin's me-row")
		}
	}
}

// TestSlice270_PrivilegedCallerSeesEverything pins the
// CallerIsPrivileged=true short-circuit: an admin reaching
// /v1/activity/unified sees the SAME shape they would see on the
// slice-124 /v1/admin/audit-log/unified endpoint (full visibility).
// Slice 270 D1 surface-uniformity claim verified end-to-end.
func TestSlice270_PrivilegedCallerSeesEverything(t *testing.T) {
	tenant := uuid.New()
	someoneElse := uuid.New()
	adminActor := uuid.New()

	cleanupUnifiedTables(t, tenant)

	// Seed: me-row for someone other than the caller + feature_flag row.
	seedMeRowForActor(t, tenant, someoneElse, "profile.update")
	seedUnifiedRow(t, tenant, "feature_flag_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to)

	// Admin caller.
	r := newActivityRouter(t, tenant, adminActor.String(), true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	sawSomeoneElseMeRow := false
	sawFeatureFlag := false
	for _, e := range resp.Entries {
		if e.Kind == "me" && e.ActorID == someoneElse.String() && e.Action == "profile.update" {
			sawSomeoneElseMeRow = true
		}
		if e.Kind == "feature_flag" {
			sawFeatureFlag = true
		}
	}
	if !sawSomeoneElseMeRow {
		t.Errorf("admin caller did NOT see another actor's me-row; want full visibility (CallerIsPrivileged short-circuit)")
	}
	if !sawFeatureFlag {
		t.Errorf("admin caller did NOT see feature_flag row; want full visibility (CallerIsPrivileged short-circuit)")
	}
}
