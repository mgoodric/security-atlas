//go:build integration

// Integration tests for slice 688 — vendor_reviews ledger. Real Postgres
// only; RLS cannot be tested against a fake DB. Layout mirrors
// integration_get_delete_test.go.
//
// Coverage (AC-6):
//   - RecordReview happy-path round-trip + last_review_date consistency.
//   - ListReviews orders newest-first.
//   - RLS cross-tenant denial on READ (B cannot see A's reviews).
//   - RLS cross-tenant denial on WRITE (B cannot record against A's vendor —
//     the FK is invisible to B, so the insert maps to ErrVendorNotFound).
//   - Back-fill: exactly one ledger row per pre-existing last_review_date.

package vendor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// TestRecordReview_RoundTrip — record a review, read it back, and confirm the
// vendor's last_review_date scalar is bumped to the review date (AC-2 / D2).
func TestRecordReview_RoundTrip(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Reviewed Co",
		Criticality:   vendor.CriticalityHigh,
		ReviewCadence: vendor.CadenceQuarterly,
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	reviewedAt := parseDate(t, "2026-03-15")
	rv, err := store.RecordReview(ctx, vendor.RecordReviewInput{
		VendorID:   v.ID,
		ReviewedAt: reviewedAt,
		Reviewer:   "owner@demo.example",
		Outcome:    vendor.OutcomePassWithFindings,
		Notes:      "SOC 2 Type II received; one low finding tracked.",
	})
	if err != nil {
		t.Fatalf("record review: %v", err)
	}
	if rv.ID == uuid.Nil {
		t.Fatalf("review id should be set")
	}
	if rv.Outcome != vendor.OutcomePassWithFindings {
		t.Fatalf("outcome = %q; want pass_with_findings", rv.Outcome)
	}
	if !rv.ReviewedAt.Equal(reviewedAt) {
		t.Fatalf("reviewed_at = %v; want %v", rv.ReviewedAt, reviewedAt)
	}

	// The scalar must now reflect the review.
	got, err := store.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("get vendor: %v", err)
	}
	if got.LastReviewDate == nil || !got.LastReviewDate.Equal(reviewedAt) {
		t.Fatalf("last_review_date = %v; want %v", got.LastReviewDate, reviewedAt)
	}
}

// TestRecordReview_BackdatedDoesNotRegressScalar — a back-dated review never
// pulls last_review_date below an existing newer review (the scalar tracks
// the MOST-RECENT reviewed_at, not the last-recorded).
func TestRecordReview_BackdatedDoesNotRegressScalar(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Backdate Co",
		Criticality:   vendor.CriticalityMedium,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	newer := parseDate(t, "2026-05-01")
	older := parseDate(t, "2026-01-01")
	if _, err := store.RecordReview(ctx, vendor.RecordReviewInput{
		VendorID: v.ID, ReviewedAt: newer, Outcome: vendor.OutcomePass,
	}); err != nil {
		t.Fatalf("record newer: %v", err)
	}
	if _, err := store.RecordReview(ctx, vendor.RecordReviewInput{
		VendorID: v.ID, ReviewedAt: older, Outcome: vendor.OutcomePass,
	}); err != nil {
		t.Fatalf("record older: %v", err)
	}

	got, err := store.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("get vendor: %v", err)
	}
	if got.LastReviewDate == nil || !got.LastReviewDate.Equal(newer) {
		t.Fatalf("last_review_date = %v; want %v (newer, not regressed)", got.LastReviewDate, newer)
	}
}

// TestListReviews_NewestFirst — AC-3 ordering. Three reviews recorded out of
// chronological order come back newest reviewed_at first.
func TestListReviews_NewestFirst(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Timeline Co",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceQuarterly,
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	dates := []string{"2026-01-01", "2026-06-01", "2026-03-01"}
	for _, d := range dates {
		if _, err := store.RecordReview(ctx, vendor.RecordReviewInput{
			VendorID: v.ID, ReviewedAt: parseDate(t, d), Outcome: vendor.OutcomePass,
		}); err != nil {
			t.Fatalf("record %s: %v", d, err)
		}
	}

	reviews, err := store.ListReviews(ctx, v.ID)
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 3 {
		t.Fatalf("want 3 reviews; got %d", len(reviews))
	}
	want := []string{"2026-06-01", "2026-03-01", "2026-01-01"}
	for i, w := range want {
		if got := reviews[i].ReviewedAt.Format("2006-01-02"); got != w {
			t.Fatalf("reviews[%d].reviewed_at = %s; want %s (newest-first)", i, got, w)
		}
	}
}

// TestListReviews_RLSIsolatesCrossTenant — Tenant B cannot read Tenant A's
// review history; the RLS predicate makes A's rows invisible, so B sees an
// empty series (never a leak).
func TestListReviews_RLSIsolatesCrossTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := vendor.NewStore(app)

	ctxA := tenantCtx(t, tenantA)
	v, err := store.Create(ctxA, vendor.CreateVendorInput{
		Name:          "A-Reviewed",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create as A: %v", err)
	}
	if _, err := store.RecordReview(ctxA, vendor.RecordReviewInput{
		VendorID: v.ID, ReviewedAt: parseDate(t, "2026-02-01"), Outcome: vendor.OutcomePass,
	}); err != nil {
		t.Fatalf("record as A: %v", err)
	}

	// B lists the SAME vendor id — RLS denies; empty series, no error.
	ctxB, err := tenancy.WithTenant(context.Background(), tenantB)
	if err != nil {
		t.Fatalf("WithTenant B: %v", err)
	}
	reviews, err := store.ListReviews(ctxB, v.ID)
	if err != nil {
		t.Fatalf("cross-tenant list should be empty, not error; got %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("cross-tenant list should be empty; got %d rows", len(reviews))
	}
}

// TestRecordReview_RLSIsolatesCrossTenantWrite — Tenant B cannot record a
// review against Tenant A's vendor. A's vendor row is invisible to B, so the
// composite FK (tenant_id, vendor_id) -> vendors finds no parent under B's
// tenant and the insert maps to ErrVendorNotFound. A's history is untouched.
func TestRecordReview_RLSIsolatesCrossTenantWrite(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := vendor.NewStore(app)

	ctxA := tenantCtx(t, tenantA)
	v, err := store.Create(ctxA, vendor.CreateVendorInput{
		Name:          "A-Target",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create as A: %v", err)
	}

	ctxB, err := tenancy.WithTenant(context.Background(), tenantB)
	if err != nil {
		t.Fatalf("WithTenant B: %v", err)
	}
	if _, err := store.RecordReview(ctxB, vendor.RecordReviewInput{
		VendorID: v.ID, ReviewedAt: parseDate(t, "2026-04-01"), Outcome: vendor.OutcomePass,
	}); !errors.Is(err, vendor.ErrVendorNotFound) {
		t.Fatalf("cross-tenant write should be ErrVendorNotFound; got %v", err)
	}

	// A's history must remain empty — B's attempt recorded nothing.
	reviews, err := store.ListReviews(ctxA, v.ID)
	if err != nil {
		t.Fatalf("list as A: %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("A's history should be empty after B's denied write; got %d", len(reviews))
	}
}

// TestBackfill_OneRowPerLastReviewDate — AC-6 back-fill assertion. The
// migration seeded exactly one ledger row per pre-existing non-null
// last_review_date. We re-create that situation in-test: insert a vendor with
// a last_review_date directly (admin pool, bypassing the ledger), then run the
// back-fill INSERT the migration uses, and assert exactly one row results.
//
// We exercise the migration's back-fill SQL rather than asserting against
// whatever the live DB happened to back-fill, so the test is hermetic per
// tenant.
func TestBackfill_OneRowPerLastReviewDate(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	// Two vendors WITH a last_review_date, one WITHOUT.
	withReviewA, err := store.Create(ctx, vendor.CreateVendorInput{
		Name: "Has Review A", Criticality: vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual, LastReviewDate: ptr(parseDate(t, "2026-01-10")),
	})
	if err != nil {
		t.Fatalf("create withReviewA: %v", err)
	}
	withReviewB, err := store.Create(ctx, vendor.CreateVendorInput{
		Name: "Has Review B", Criticality: vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual, LastReviewDate: ptr(parseDate(t, "2026-02-20")),
	})
	if err != nil {
		t.Fatalf("create withReviewB: %v", err)
	}
	noReview, err := store.Create(ctx, vendor.CreateVendorInput{
		Name: "No Review", Criticality: vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create noReview: %v", err)
	}

	// Run the migration's back-fill SQL scoped to this tenant (admin pool
	// bypasses RLS, mirroring atlas_migrate's BYPASSRLS during the real
	// migration). Idempotent-safe to the test's own tenant.
	backfill := `
		INSERT INTO vendor_reviews (id, tenant_id, vendor_id, reviewed_at, reviewer, outcome, notes)
		SELECT gen_random_uuid(), v.tenant_id, v.id, v.last_review_date, '', 'pass',
		       'Back-filled from vendor.last_review_date (slice 688 migration).'
		FROM vendors v
		WHERE v.last_review_date IS NOT NULL AND v.tenant_id = $1`
	if _, err := admin.Exec(context.Background(), backfill, tenant); err != nil {
		t.Fatalf("back-fill exec: %v", err)
	}

	// Exactly one ledger row per vendor that had a last_review_date.
	for _, tc := range []struct {
		name     string
		id       uuid.UUID
		wantRows int
		wantDate string
	}{
		{"withReviewA", withReviewA.ID, 1, "2026-01-10"},
		{"withReviewB", withReviewB.ID, 1, "2026-02-20"},
		{"noReview", noReview.ID, 0, ""},
	} {
		reviews, err := store.ListReviews(ctx, tc.id)
		if err != nil {
			t.Fatalf("%s list: %v", tc.name, err)
		}
		if len(reviews) != tc.wantRows {
			t.Fatalf("%s: want %d back-filled rows; got %d", tc.name, tc.wantRows, len(reviews))
		}
		if tc.wantRows == 1 {
			if got := reviews[0].ReviewedAt.Format("2006-01-02"); got != tc.wantDate {
				t.Fatalf("%s: back-filled reviewed_at = %s; want %s", tc.name, got, tc.wantDate)
			}
			if reviews[0].Outcome != vendor.OutcomePass {
				t.Fatalf("%s: back-filled outcome = %q; want pass", tc.name, reviews[0].Outcome)
			}
		}
	}
}
