//go:build integration

// Integration tests for the Get + Delete store methods — added by slice 287
// to close the per-function coverage gap surfaced by the slice 279 audit.
// The slice-024 integration_test.go covered Create / Update / List /
// Burndown but never exercised Get or Delete directly (callers Get via
// the round-trip return of Create, and Delete had no test at all).
//
// Load-bearing functions covered here (slice 287):
//
//   - Store.Get — happy-path round-trip + ErrVendorNotFound on miss +
//     RLS isolation on cross-tenant probe.
//   - Store.Delete — happy-path removal + idempotent re-delete (no error).

package vendor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// TestGetVendor_RoundTrip — Create then Get and verify every field
// hydrates back the way it went in.
func TestGetVendor_RoundTrip(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	created, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:           "RoundTrip",
		Domain:         ptr("roundtrip.example"),
		Criticality:    vendor.CriticalityMedium,
		ReviewCadence:  vendor.CadenceQuarterly,
		LastReviewDate: ptr(parseDate(t, "2026-03-01")),
		OwnerUser:      "alice@example.com",
		Notes:          "round-trip test",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("id mismatch: %v vs %v", got.ID, created.ID)
	}
	if got.Name != "RoundTrip" {
		t.Fatalf("name mismatch: %q", got.Name)
	}
	if got.Domain == nil || *got.Domain != "roundtrip.example" {
		t.Fatalf("domain mismatch: %v", got.Domain)
	}
	if got.Criticality != vendor.CriticalityMedium {
		t.Fatalf("criticality mismatch: %q", got.Criticality)
	}
	if got.ReviewCadence != vendor.CadenceQuarterly {
		t.Fatalf("cadence mismatch: %q", got.ReviewCadence)
	}
	if got.LastReviewDate == nil || !got.LastReviewDate.Equal(parseDate(t, "2026-03-01")) {
		t.Fatalf("last review mismatch: %v", got.LastReviewDate)
	}
}

// TestGetVendor_NotFound — Get on a fabricated UUID returns
// ErrVendorNotFound, not an opaque pgx error.
func TestGetVendor_NotFound(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	_, err := store.Get(ctx, uuid.New())
	if !errors.Is(err, vendor.ErrVendorNotFound) {
		t.Fatalf("want ErrVendorNotFound; got %v", err)
	}
}

// TestGetVendor_RLSIsolatesCrossTenant — Tenant B cannot Get a vendor
// created by Tenant A. The DB-layer RLS predicate denies the row; the
// store surfaces ErrVendorNotFound, not the row.
func TestGetVendor_RLSIsolatesCrossTenant(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := vendor.NewStore(app)

	// Create as A.
	ctxA := tenantCtx(t, tenantA)
	created, err := store.Create(ctxA, vendor.CreateVendorInput{
		Name:          "A-Vendor",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create as A: %v", err)
	}

	// Try Get as B — must miss.
	ctxB, err := tenancy.WithTenant(context.Background(), tenantB)
	if err != nil {
		t.Fatalf("WithTenant B: %v", err)
	}
	if _, err := store.Get(ctxB, created.ID); !errors.Is(err, vendor.ErrVendorNotFound) {
		t.Fatalf("cross-tenant Get should be NotFound; got %v", err)
	}
}

// TestDeleteVendor_Removes — Create then Delete then Get returns
// ErrVendorNotFound. The CASCADE on vendor_scope_cells means we do not
// have to clean up scope-cell bindings separately.
func TestDeleteVendor_Removes(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	created, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "DeleteMe",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, created.ID); !errors.Is(err, vendor.ErrVendorNotFound) {
		t.Fatalf("post-delete Get should be NotFound; got %v", err)
	}
}

// TestDeleteVendor_IdempotentOnMissingRow — Delete on a fabricated UUID
// returns nil (Delete is idempotent by design; the SQL DELETE is a
// no-op on a missing row).
func TestDeleteVendor_IdempotentOnMissingRow(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	if err := store.Delete(ctx, uuid.New()); err != nil {
		t.Fatalf("Delete on missing row should be a no-op; got %v", err)
	}
}

// TestStore_InTx_RejectsMissingTenantContext — exercises the inTx error
// path when the caller forgets to set tenancy.WithTenant on the context.
// Without this guard, a bug in the API layer could leak a query into the
// "no tenant set" rabbit hole; the store rejects it before any SQL runs.
func TestStore_InTx_RejectsMissingTenantContext(t *testing.T) {
	app := openPool(t, appDSN(t))
	defer app.Close()
	store := vendor.NewStore(app)

	// Bare context with no tenant — must reject.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := store.Get(ctx, uuid.New())
	if err == nil {
		t.Fatalf("Get with no tenant context should error; got nil")
	}
	// We don't assert on the exact error text — tenancy.TenantFromContext
	// owns the wording. The contract is: an error is returned.
}
