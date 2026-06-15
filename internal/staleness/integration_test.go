//go:build integration

// Integration tests for slice 439: evidence-staleness rollup PRODUCER. Real
// Postgres only — RLS tenant isolation (the load-bearing AC-11 test), the
// slice-029 notification writes, the slice-439 idempotency ledger, and the
// per-user in_app opt-out are only meaningful against a real database. The DB
// is never mocked.
//
// Run with: go test -tags=integration -p 1 ./internal/staleness/...
//
// Required env:
//   DATABASE_URL      - migrator role DSN (BYPASSRLS); seeds controls,
//                       evidence, users, prefs outside the GUC + cleans up.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       rollup Store runs against this so RLS is enforced.

package staleness_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/staleness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a new tenant id and registers cleanup of every table
// this slice touches for that tenant. Pure tenant-scoped DELETE in FK order,
// so it delegates to dbtest.SeedTenant (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"staleness_rollup_log",
		"notifications",
		"user_notification_preferences",
		"evidence_freshness",
		"evidence_records",
		"controls",
		"users",
	)
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func seedUser(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		 VALUES ($1, $2, $3, $4, 'active')`,
		id, tenant, "user-"+id.String()[:8]+"@example.test", "Test User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	var fc *string
	if freshnessClass != "" {
		fc = &freshnessClass
	}
	bundleID := "test-bundle-439-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 439 staleness test control', 'AAA', 'automated',
		        $3, $4, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID, fc); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, observedAt time.Time) {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, $6)
	`, id, tenant, ctrlID, observedAt, "hash-439-"+id.String()[:8], ctrlID.String()); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

func setInAppPref(t *testing.T, admin *pgxpool.Pool, tenant string, userID uuid.UUID, enabled bool) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO user_notification_preferences (tenant_id, user_id, event, channel, enabled)
		VALUES ($1, $2, 'evidence_staleness', 'in_app', $3)
		ON CONFLICT (tenant_id, user_id, event, channel)
		DO UPDATE SET enabled = EXCLUDED.enabled
	`, tenant, userID, enabled); err != nil {
		t.Fatalf("set in_app pref: %v", err)
	}
}

// refreshFreshness materializes the freshness read model for the tenant so the
// rollup has rows to read (the rollup is a pure consumer of the read model).
func refreshFreshness(t *testing.T, app *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	if _, err := freshness.NewStore(app).Refresh(ctx); err != nil {
		t.Fatalf("freshness Refresh: %v", err)
	}
}

// listNotifications returns the staleness notifications for a recipient (admin
// pool, GUC-set so RLS is satisfied) — the assertion surface.
func listNotifications(t *testing.T, admin *pgxpool.Pool, tenant string, recipient uuid.UUID) []notificationRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT type, payload FROM notifications
		WHERE tenant_id = $1 AND recipient_user_id = $2
		ORDER BY created_at ASC
	`, tenant, recipient.String())
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	defer rows.Close()
	var out []notificationRow
	for rows.Next() {
		var nr notificationRow
		var raw []byte
		if err := rows.Scan(&nr.Type, &raw); err != nil {
			t.Fatalf("scan notification: %v", err)
		}
		_ = json.Unmarshal(raw, &nr.Payload)
		out = append(out, nr)
	}
	return out
}

type notificationRow struct {
	Type    string
	Payload map[string]any
}

// ===== AC-10: the rollup writes the expected staleness notifications =====

func TestRollup_WritesStalenessAlertForStaleControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	user := seedUser(t, admin, tenant)

	// weekly class = 30d max-age; evidence observed 60 days ago -> stale.
	ctrl := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().Add(-60*24*time.Hour))
	refreshFreshness(t, app, ctx)

	store := staleness.NewStore(app, freshness.NewStore(app))
	rep, err := store.RollupTenant(ctx, false /* not weekly: alerts only */)
	if err != nil {
		t.Fatalf("RollupTenant: %v", err)
	}
	if rep.StaleControls != 1 {
		t.Errorf("StaleControls = %d, want 1", rep.StaleControls)
	}
	if rep.AlertsWritten != 1 {
		t.Errorf("AlertsWritten = %d, want 1", rep.AlertsWritten)
	}

	notes := listNotifications(t, admin, tenant, user)
	if len(notes) != 1 {
		t.Fatalf("got %d notifications, want 1", len(notes))
	}
	if notes[0].Type != notifications.TypeEvidenceStaleness {
		t.Errorf("notification type = %q, want %q", notes[0].Type, notifications.TypeEvidenceStaleness)
	}
	if notes[0].Payload["subtype"] != "alert" {
		t.Errorf("subtype = %v, want alert", notes[0].Payload["subtype"])
	}
	if notes[0].Payload["band"] != "stale" {
		t.Errorf("band = %v, want stale", notes[0].Payload["band"])
	}
	// honest-interval copy must be present (AC-6 / P0-439-1).
	msg, _ := notes[0].Payload["message"].(string)
	if msg == "" || !containsInterval(msg) {
		t.Errorf("alert message must name the recompute interval honestly; got %q", msg)
	}
}

func TestRollup_FreshControlProducesNoNotification(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	user := seedUser(t, admin, tenant)

	ctrl := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().Add(-2*24*time.Hour)) // 2d -> fresh
	refreshFreshness(t, app, ctx)

	store := staleness.NewStore(app, freshness.NewStore(app))
	rep, err := store.RollupTenant(ctx, true)
	if err != nil {
		t.Fatalf("RollupTenant: %v", err)
	}
	if rep.StaleControls != 0 || rep.AlertsWritten != 0 {
		t.Errorf("fresh control should produce no alert; rep=%+v", rep)
	}
	if notes := listNotifications(t, admin, tenant, user); len(notes) != 0 {
		t.Errorf("fresh control produced %d notifications, want 0", len(notes))
	}
}

// ===== AC-4: the weekly digest summarizes stale + approaching =====

func TestRollup_WeeklyDigestSummarizes(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	user := seedUser(t, admin, tenant)

	// one stale (60d), one fresh-but-approaching: weekly=30d, observed 20d ago
	// -> valid_until 10d out, inside the 14d approaching window.
	stale := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, stale, time.Now().UTC().Add(-60*24*time.Hour))
	appr := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, appr, time.Now().UTC().Add(-20*24*time.Hour))
	refreshFreshness(t, app, ctx)

	store := staleness.NewStore(app, freshness.NewStore(app))
	rep, err := store.RollupTenant(ctx, true /* weekly */)
	if err != nil {
		t.Fatalf("RollupTenant: %v", err)
	}
	if rep.StaleControls != 1 || rep.ApprControls != 1 {
		t.Errorf("expected 1 stale + 1 approaching; rep=%+v", rep)
	}
	if rep.DigestsWritten != 1 {
		t.Errorf("DigestsWritten = %d, want 1", rep.DigestsWritten)
	}

	notes := listNotifications(t, admin, tenant, user)
	var digest *notificationRow
	for i := range notes {
		if notes[i].Payload["subtype"] == "digest" {
			digest = &notes[i]
		}
	}
	if digest == nil {
		t.Fatal("no digest notification written")
	}
	if got := digest.Payload["stale_count"]; got != float64(1) {
		t.Errorf("digest stale_count = %v, want 1", got)
	}
	if got := digest.Payload["approaching_count"]; got != float64(1) {
		t.Errorf("digest approaching_count = %v, want 1", got)
	}
	// must state the period it covers + the honest cadence (AC-4 / AC-6).
	if digest.Payload["period_start"] == nil || digest.Payload["period_end"] == nil {
		t.Error("digest must state the period it covers")
	}
	if digest.Payload["freshness_view_url"] != staleness.FreshnessViewPath {
		t.Errorf("digest must link to the freshness view (AC-9); got %v", digest.Payload["freshness_view_url"])
	}
}

// ===== AC-11: TENANT ISOLATION — the load-bearing test (threat-model I) =====

func TestRollup_TenantIsolation_NoCrossTenantLeak(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	// Tenant A has a stale control + a user. Tenant B has a user but NO stale
	// evidence. After running BOTH tenants' rollups, Tenant B's user must have
	// ZERO notifications — Tenant A's stale facts must never appear in B.
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctxA := ctxFor(t, tenantA)
	ctxB := ctxFor(t, tenantB)

	userA := seedUser(t, admin, tenantA)
	userB := seedUser(t, admin, tenantB)

	ctrlA := seedControl(t, admin, tenantA, "weekly")
	seedEvidence(t, admin, tenantA, ctrlA, time.Now().UTC().Add(-90*24*time.Hour)) // very stale
	refreshFreshness(t, app, ctxA)
	// Tenant B: a control with FRESH evidence (nothing to alert on).
	ctrlB := seedControl(t, admin, tenantB, "weekly")
	seedEvidence(t, admin, tenantB, ctrlB, time.Now().UTC().Add(-1*24*time.Hour))
	refreshFreshness(t, app, ctxB)

	store := staleness.NewStore(app, freshness.NewStore(app))

	repA, err := store.RollupTenant(ctxA, true)
	if err != nil {
		t.Fatalf("RollupTenant A: %v", err)
	}
	repB, err := store.RollupTenant(ctxB, true)
	if err != nil {
		t.Fatalf("RollupTenant B: %v", err)
	}

	// Tenant A's user got the alert + digest.
	if repA.AlertsWritten < 1 {
		t.Errorf("tenant A should have alerts; rep=%+v", repA)
	}
	notesA := listNotifications(t, admin, tenantA, userA)
	if len(notesA) == 0 {
		t.Fatal("tenant A user should have notifications")
	}

	// THE LOAD-BEARING ASSERTION: Tenant B's user has ZERO notifications.
	// Tenant A's stale control never leaked into B.
	notesB := listNotifications(t, admin, tenantB, userB)
	if len(notesB) != 0 {
		t.Fatalf("CROSS-TENANT LEAK: tenant B user has %d notifications, want 0 (repB=%+v)", len(notesB), repB)
	}

	// Belt-and-suspenders: assert no notification anywhere references tenant A's
	// control id under tenant B.
	for _, n := range notesB {
		if cid, _ := n.Payload["control_id"].(string); cid == ctrlA.String() {
			t.Fatalf("CROSS-TENANT LEAK: tenant A control %s appears in tenant B notification", ctrlA)
		}
	}
}

// A second isolation angle: a rollup run while the GUC is set to tenant A must
// never address tenant B's USERS — even though B has its own stale evidence.
func TestRollup_DoesNotAddressOtherTenantUsers(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctxA := ctxFor(t, tenantA)

	_ = seedUser(t, admin, tenantA)
	userB := seedUser(t, admin, tenantB)

	ctrlA := seedControl(t, admin, tenantA, "weekly")
	seedEvidence(t, admin, tenantA, ctrlA, time.Now().UTC().Add(-90*24*time.Hour))
	refreshFreshness(t, app, ctxA)

	store := staleness.NewStore(app, freshness.NewStore(app))
	rep, err := store.RollupTenant(ctxA, true)
	if err != nil {
		t.Fatalf("RollupTenant A: %v", err)
	}
	// Recipients enumerated under tenant A's GUC must be exactly tenant A's
	// users (1), never include tenant B's user.
	if rep.Recipients != 1 {
		t.Errorf("tenant A rollup enumerated %d recipients, want 1 (B's user must be invisible)", rep.Recipients)
	}
	if notes := listNotifications(t, admin, tenantB, userB); len(notes) != 0 {
		t.Errorf("tenant B user received %d notifications from tenant A's rollup, want 0", len(notes))
	}
}

// ===== AC-12: idempotency — a second run produces no duplicates =====

func TestRollup_Idempotent_NoDuplicateOnRerun(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	user := seedUser(t, admin, tenant)

	ctrl := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().Add(-60*24*time.Hour))
	refreshFreshness(t, app, ctx)

	store := staleness.NewStore(app, freshness.NewStore(app))

	rep1, err := store.RollupTenant(ctx, true)
	if err != nil {
		t.Fatalf("RollupTenant run 1: %v", err)
	}
	if rep1.AlertsWritten != 1 || rep1.DigestsWritten != 1 {
		t.Fatalf("run 1 should write 1 alert + 1 digest; rep=%+v", rep1)
	}

	// Second run in the same recompute period + same ISO-week: everything
	// deduped, nothing newly written.
	rep2, err := store.RollupTenant(ctx, true)
	if err != nil {
		t.Fatalf("RollupTenant run 2: %v", err)
	}
	if rep2.AlertsWritten != 0 || rep2.DigestsWritten != 0 {
		t.Errorf("run 2 should write nothing (idempotent); rep=%+v", rep2)
	}
	if rep2.AlertsDeduped != 1 || rep2.DigestsDeduped != 1 {
		t.Errorf("run 2 should dedup 1 alert + 1 digest; rep=%+v", rep2)
	}

	// Exactly one alert + one digest notification total.
	notes := listNotifications(t, admin, tenant, user)
	if len(notes) != 2 {
		t.Fatalf("after two runs got %d notifications, want 2 (1 alert + 1 digest)", len(notes))
	}
}

// ===== AC-7: per-user in_app opt-out is honored =====

func TestRollup_OptOutSuppressesDelivery(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	user := seedUser(t, admin, tenant)
	setInAppPref(t, admin, tenant, user, false) // explicit opt-out

	ctrl := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().Add(-60*24*time.Hour))
	refreshFreshness(t, app, ctx)

	store := staleness.NewStore(app, freshness.NewStore(app))
	rep, err := store.RollupTenant(ctx, true)
	if err != nil {
		t.Fatalf("RollupTenant: %v", err)
	}
	if rep.AlertsWritten != 0 || rep.DigestsWritten != 0 {
		t.Errorf("opted-out user should receive nothing; rep=%+v", rep)
	}
	if notes := listNotifications(t, admin, tenant, user); len(notes) != 0 {
		t.Errorf("opted-out user has %d notifications, want 0", len(notes))
	}
}

// ===== AC-1: the scheduler enumerates tenants + drives the rollup =====
//
// Exercises the full producer path through the real scheduler: the migrator
// pool enumerates tenants (cross-tenant key read), the app pool runs each
// tenant's rollup under its own GUC. Re-asserts the cross-tenant boundary at
// the scheduler altitude (not just the store).

func TestScheduler_SweepOnce_DrivesRollupPerTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	userA := seedUser(t, admin, tenantA)
	userB := seedUser(t, admin, tenantB)

	// A: stale evidence; B: fresh.
	ctrlA := seedControl(t, admin, tenantA, "weekly")
	seedEvidence(t, admin, tenantA, ctrlA, time.Now().UTC().Add(-90*24*time.Hour))
	refreshFreshness(t, app, ctxFor(t, tenantA))
	ctrlB := seedControl(t, admin, tenantB, "weekly")
	seedEvidence(t, admin, tenantB, ctrlB, time.Now().UTC().Add(-1*24*time.Hour))
	refreshFreshness(t, app, ctxFor(t, tenantB))

	// migratorPool = admin (BYPASSRLS), appPool = app (RLS-enforced).
	sched := staleness.New(admin, app, nil)
	rep, err := sched.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.TenantFailures != 0 {
		t.Errorf("SweepOnce had %d tenant failures; rep=%+v", rep.TenantFailures, rep)
	}
	if rep.AlertsWritten < 1 {
		t.Errorf("SweepOnce should write at least one alert (tenant A stale); rep=%+v", rep)
	}

	// Tenant A's user got notifications; tenant B's user (fresh) got none —
	// the cross-tenant boundary holds through the scheduler.
	if notes := listNotifications(t, admin, tenantA, userA); len(notes) == 0 {
		t.Error("tenant A user should have notifications after sweep")
	}
	if notes := listNotifications(t, admin, tenantB, userB); len(notes) != 0 {
		t.Errorf("tenant B user (fresh) has %d notifications after sweep, want 0", len(notes))
	}
}

func containsInterval(s string) bool {
	return strings.Contains(s, staleness.RecomputeIntervalText) || strings.Contains(s, staleness.DigestCadenceText)
}
