//go:build integration

// Slice 476 — demo-data operator-reachability journey (end-to-end).
//
// This file proves the slice-476 journey that no single prior slice tests on
// its own. The mechanism is already shipped:
//
//   - slice 205/278 : the demo seeder creates a SEPARATE tenant and populates
//     it (50 controls / 20 risks / 200 evidence).
//   - slice 478     : POST /v1/admin/users/assign (self_assign=true) creates the
//     seeding operator's membership + role(s) in any tenant the super_admin
//     flag authorizes, including the local-auth synthetic-key case.
//   - slice 192     : the DBUserResolver enumerates that membership into the
//     JWT's available_tenants, which the tenant-switcher reads.
//
// Slices 205, 478, and 192 are each tested in isolation. The slice-476 gap was
// that the COMPOSED journey — seed the REAL demo dataset, then self-assign the
// seeding operator to that REAL demo tenant, then prove it is reachable WITH the
// 50/20/200 dataset present — was never asserted end-to-end. After the 478/479
// reduction (the assign API + admin UI subsumed slice 476's original mechanism),
// slice 476 reduces to exactly this proving test.
//
// Why integration (not e2e): the journey's load-bearing parts are server/DB
// surfaces — the seeder's cross-tenant BYPASSRLS write, the slice-478 synthetic
// local-auth key, and the slice-192 resolver enumeration. The two UI surfaces
// the journey ends in (the slice-479 /admin/users page and the slice-192
// tenant-switcher) are already e2e-covered (web/e2e/admin-users.spec.ts,
// web/e2e/tenant-switch.spec.ts, web/e2e/admin-demo.spec.ts) and are thin
// wrappers over these exact APIs. Asserting the composed journey at the data
// layer is where the real proof lives; re-driving it through chromedp would add
// flake surface without adding coverage of anything the existing specs miss.
//
// Slice 476 acceptance criteria mapped:
//
//	AC-1 : after seeding, the seeding operator reaches the demo tenant — proven
//	       by the demo tenant appearing in the resolver's available_tenants AND
//	       the 50/20/200 dataset being present in that tenant.
//	AC-2 : works for a LOCAL-AUTH operator (empty-IdP identity) — the
//	       load-bearing case; TestDemoReachable_LocalAuthOperator.
//	AC-3 : the P0-192-5 membership bound is UNCHANGED for non-super-admins — a
//	       normal user who did NOT self-assign cannot see the demo tenant;
//	       TestDemoReachable_NormalUserCannotSeeDemoTenant.
//	AC-5 : switching is explicit — the resolver surfaces the tenant as
//	       AVAILABLE but never changes current_tenant; asserted in AC-1/AC-2.
//
// Reuses the slice-478 harness in this same _test package: newRouter478,
// seedTenant478, seedUser478, seedSuperAdmin, doJSON, containsTenant, appPool,
// adminPool. Demo data is seeded via the real internal/demoseed.Seeder.
//
// All fixtures use neutral test-* strings (P0-478-6 / P0-A7). The seeded demo
// tenant uses a unique per-test slug (NOT the literal "demo") so a shared CI
// database is never polluted and parallel runs cannot collide; t.Cleanup tears
// it down via the seeder's own Teardown (AC-6 interplay is exercised).

package adminusers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// seedDemoDataset runs the REAL slice-205 demo seeder against a unique neutral
// slug and returns the created demo tenant id. Registers a t.Cleanup that tears
// the demo tenant down via the seeder's own Teardown (exercising the AC-6
// seed/teardown interplay). Skips the test when the BYPASSRLS pool is absent.
func seedDemoDataset(t *testing.T, actorID, actorTenant uuid.UUID) (uuid.UUID, demoseed.Result) {
	t.Helper()
	skipIfNoAdminPool(t)

	// Unique neutral slug: the demo seeder is idempotent on slug, so a fixed
	// slug would collide across parallel runs / a shared CI DB. validateSlug
	// requires a slug shape; "test-demo-<hex>" is neutral and unique.
	slug := fmt.Sprintf("test-demo-%s", uuid.New().String()[:8])

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("new seeder: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{
		Slug:          slug,
		ActorUserID:   actorID,
		ActorTenantID: actorTenant,
	})
	if err != nil {
		t.Fatalf("seed demo dataset: %v", err)
	}
	if res.Idempotent {
		t.Fatalf("expected a fresh seed, got Idempotent=true for slug %q", slug)
	}

	t.Cleanup(func() {
		// Teardown removes the demo tenant + all its rows + the membership
		// grant (the users row in the demo tenant lives under that tenant_id
		// and is cascade-deleted). Best-effort: a teardown failure should not
		// mask the test result.
		_ = seeder.Teardown(context.Background(), slug, actorID, actorTenant)
	})

	return res.TenantID, res
}

// countTenantRows counts rows in the given table for a tenant via the BYPASSRLS
// pool (the seeder's own write surface).
func countTenantRows(t *testing.T, table string, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, table),
		tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// assertDemoDatasetPresent asserts the seeded demo tenant carries the
// headline 50/20/200 dataset the operator expects to reach (AC-1). The seeder
// guarantees these floors at DefaultScale (slice 205 AC-1/AC-6).
func assertDemoDatasetPresent(t *testing.T, demoTenant uuid.UUID, res demoseed.Result) {
	t.Helper()
	controls := countTenantRows(t, "controls", demoTenant)
	risks := countTenantRows(t, "risks", demoTenant)
	evidence := countTenantRows(t, "evidence_records", demoTenant)

	if controls < 50 {
		t.Errorf("demo tenant has %d controls; want >= 50 (Result reported %d)", controls, res.Controls)
	}
	if risks < 20 {
		t.Errorf("demo tenant has %d risks; want >= 20 (Result reported %d)", risks, res.Risks)
	}
	if evidence < 200 {
		t.Errorf("demo tenant has %d evidence_records; want >= 200 (Result reported %d)", evidence, res.Evidence)
	}
}

// TestDemoReachable_IdPOperator proves the full slice-476 journey for an
// IdP-backed super-admin operator (AC-1 / AC-5):
//
//	seed real demo dataset -> self-assign to the demo tenant ->
//	demo tenant is in the operator's available_tenants ->
//	the demo tenant carries the 50/20/200 dataset.
func TestDemoReachable_IdPOperator(t *testing.T) {
	skipIfNoAdminPool(t)

	// The operator: an IdP-backed super-admin whose home row lives in their
	// session (bootstrap) tenant.
	sessionTenant := seedTenant478(t, "test-demo-idp-session")
	actorID := seedUser478(t, sessionTenant, "test-demo-idp@example.com", "Demo IdP Operator",
		"https://idp.test/", "test-demo-idp-sub")
	seedSuperAdmin(t, actorID)

	// Step 1 — seed the REAL demo dataset (a separate tenant).
	demoTenant, res := seedDemoDataset(t, actorID, sessionTenant)
	if demoTenant == sessionTenant {
		t.Fatalf("demo seeder must create a SEPARATE tenant; got the session tenant %s", sessionTenant)
	}

	// Before self-assign, the demo tenant is NOT reachable (membership bound).
	resolver := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	pre, err := resolver.ResolveForOAuth(context.Background(), actorID, sessionTenant)
	if err != nil {
		t.Fatalf("pre-assign resolve: %v", err)
	}
	if containsTenant(pre.AvailableTenants, demoTenant) {
		t.Fatalf("demo tenant %s reachable BEFORE self-assign — seed must not grant access implicitly", demoTenant)
	}

	// Step 2 — the operator self-assigns to the demo tenant (slice-478 path).
	h := newRouter478(t, sessionTenant, actorID, true)
	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"self_assign": true,
		"tenant_id":   demoTenant.String(),
		"roles":       []string{"admin"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("self-assign to demo tenant status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// Step 3 — the demo tenant is now reachable: it appears in the operator's
	// available_tenants (AC-1). current_tenant is UNCHANGED — the switch is
	// explicit (AC-5): the resolver surfaces availability, never auto-switches.
	post, err := resolver.ResolveForOAuth(context.Background(), actorID, sessionTenant)
	if err != nil {
		t.Fatalf("post-assign resolve: %v", err)
	}
	if !containsTenant(post.AvailableTenants, demoTenant) {
		t.Errorf("demo tenant %s NOT in available_tenants %v after self-assign", demoTenant, post.AvailableTenants)
	}
	if post.CurrentTenantID != sessionTenant {
		t.Errorf("current_tenant = %s; want session tenant %s (AC-5: no auto-switch)", post.CurrentTenantID, sessionTenant)
	}

	// Step 4 — the reached demo tenant carries the 50/20/200 dataset (AC-1).
	assertDemoDatasetPresent(t, demoTenant, res)
}

// TestDemoReachable_LocalAuthOperator proves the LOAD-BEARING local-auth case
// (AC-2): a self-host operator whose identity has an EMPTY IdP tuple can still
// reach the demo tenant after self-assign. The slice-478 synthetic-key path
// (urn:atlas:local + origin user_id) makes the membership enumerable without
// the empty-tuple over-match.
func TestDemoReachable_LocalAuthOperator(t *testing.T) {
	skipIfNoAdminPool(t)

	// The operator: a LOCAL super-admin (empty idp_issuer/idp_subject) — the
	// self-host default identity shape.
	sessionTenant := seedTenant478(t, "test-demo-local-session")
	actorID := seedUser478(t, sessionTenant, "test-demo-local@example.com", "Demo Local Operator", "", "")
	seedSuperAdmin(t, actorID)

	demoTenant, res := seedDemoDataset(t, actorID, sessionTenant)

	h := newRouter478(t, sessionTenant, actorID, true)
	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"self_assign": true,
		"tenant_id":   demoTenant.String(),
		"roles":       []string{"admin"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("local-auth self-assign status = %d; body = %s", rr.Code, rr.Body.String())
	}

	resolver := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	id, err := resolver.ResolveForOAuth(context.Background(), actorID, sessionTenant)
	if err != nil {
		t.Fatalf("resolve local-auth operator: %v", err)
	}
	if !containsTenant(id.AvailableTenants, demoTenant) {
		t.Errorf("LOCAL-AUTH: demo tenant %s NOT in available_tenants %v after self-assign", demoTenant, id.AvailableTenants)
	}
	if !containsTenant(id.AvailableTenants, sessionTenant) {
		t.Errorf("LOCAL-AUTH: session tenant missing from available_tenants %v", id.AvailableTenants)
	}
	// Exactly {session, demo} — the synthetic key must not over-match other
	// local users' tenants (the slice-478 P0-478-2 hazard restated for this
	// journey).
	if len(id.AvailableTenants) != 2 {
		t.Errorf("LOCAL-AUTH: available_tenants len = %d; want 2 (no over-match) %v", len(id.AvailableTenants), id.AvailableTenants)
	}

	assertDemoDatasetPresent(t, demoTenant, res)
}

// TestDemoReachable_NormalUserCannotSeeDemoTenant proves AC-3 / P0-476-1: the
// membership-bounded picker is UNCHANGED for a normal user. A second user who
// did NOT self-assign to the demo tenant never sees it in available_tenants,
// even though the demo tenant exists and is populated.
func TestDemoReachable_NormalUserCannotSeeDemoTenant(t *testing.T) {
	skipIfNoAdminPool(t)

	// The seeding super-admin operator + a separate NORMAL user (non-super-
	// admin, never self-assigned to the demo tenant).
	opTenant := seedTenant478(t, "test-demo-bound-op")
	operatorID := seedUser478(t, opTenant, "test-demo-bound-op@example.com", "Seeding Operator",
		"https://idp.test/", "test-demo-bound-op-sub")
	seedSuperAdmin(t, operatorID)

	normalTenant := seedTenant478(t, "test-demo-bound-normal")
	normalUserID := seedUser478(t, normalTenant, "test-demo-bound-normal@example.com", "Normal User",
		"https://idp.test/", "test-demo-bound-normal-sub")

	// The operator seeds the demo dataset.
	demoTenant, _ := seedDemoDataset(t, operatorID, opTenant)

	// The normal user resolves their available_tenants. The demo tenant must
	// NOT appear — they hold no membership in it (P0-192-5 preserved).
	resolver := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	id, err := resolver.ResolveForOAuth(context.Background(), normalUserID, normalTenant)
	if err != nil {
		t.Fatalf("resolve normal user: %v", err)
	}
	if containsTenant(id.AvailableTenants, demoTenant) {
		t.Errorf("P0-476-1 VIOLATION: normal user can see demo tenant %s (available=%v)", demoTenant, id.AvailableTenants)
	}
	if containsTenant(id.AvailableTenants, opTenant) {
		t.Errorf("normal user saw the operator's tenant %s (available=%v) — membership bound broken", opTenant, id.AvailableTenants)
	}
}
