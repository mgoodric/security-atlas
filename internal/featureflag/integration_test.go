//go:build integration

// Integration tests for slice 059 -- per-tenant feature flags + capability
// toggles. Requires a real Postgres reachable via DATABASE_URL_APP. The
// tests open a tenant tx, set GUC, exercise Store, and assert RLS + audit
// log behavior end-to-end.

package featureflag_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var appPool *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping featureflag integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New: %v\n", err)
		os.Exit(1)
	}
	appPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func withTenant(t *testing.T) (context.Context, string) {
	t.Helper()
	tenant := uuid.NewString()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx, tenant
}

// TestStoreGetMissingRowReturnsSeedDefault — Store.Get with no row returns
// the Seed default (AC-A2 + ISC-39).
func TestStoreGetMissingRowReturnsSeedDefault(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, _ := withTenant(t)

	flag, err := store.Get(ctx, "risk.enabled")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	def, _ := featureflag.DefaultByKey("risk.enabled")
	if flag.Enabled != def.Enabled {
		t.Errorf("Enabled = %v; want seed default %v", flag.Enabled, def.Enabled)
	}
	if flag.HasOverride {
		t.Errorf("HasOverride = true; want false (no row yet)")
	}
}

// TestStoreSetThenGet — Set writes a row, Get returns the override.
// AC-7 + AC-9 + ISC-15.
func TestStoreSetThenGet(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, tenant := withTenant(t)

	// Risk defaults to true; flip it off.
	flag, err := store.Set(ctx, "risk.enabled", false, "test-actor", "test reason")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if flag.Enabled {
		t.Errorf("after Set(false), Enabled = true; want false")
	}
	if !flag.HasOverride {
		t.Errorf("HasOverride = false after Set; want true")
	}

	// Re-read in a fresh tx; should see the override.
	got, err := store.Get(ctx, "risk.enabled")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enabled {
		t.Errorf("Get after Set(false) = true; want false")
	}
	t.Cleanup(func() {
		ctx, _ := tenancy.WithTenant(context.Background(), tenant)
		// Best-effort cleanup -- under RLS we can only delete our own rows.
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}

// TestStoreSetWritesAuditLog — every toggle writes one feature_flag_audit_log
// row with from + to + actor + at. AC-10 + ISC-18 + ISC-36.
func TestStoreSetWritesAuditLog(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, tenant := withTenant(t)

	// vendor defaults to true; flip off then on.
	if _, err := store.Set(ctx, "vendor.enabled", false, "alice", "off-because"); err != nil {
		t.Fatalf("Set off: %v", err)
	}
	if _, err := store.Set(ctx, "vendor.enabled", true, "bob", "on-because"); err != nil {
		t.Fatalf("Set on: %v", err)
	}

	entries, err := store.AuditLog(ctx)
	if err != nil {
		t.Fatalf("AuditLog: %v", err)
	}
	// Filter for vendor.enabled (tenant is fresh so this should be 2).
	count := 0
	for _, e := range entries {
		if e.FlagKey != "vendor.enabled" {
			continue
		}
		count++
		if e.Actor == "" {
			t.Errorf("audit entry has empty actor")
		}
		if e.OccurredAt.IsZero() {
			t.Errorf("audit entry has zero occurred_at")
		}
	}
	if count != 2 {
		t.Errorf("audit log entries for vendor.enabled = %d; want 2", count)
	}

	// Newest-first ordering: vendor.enabled was seed=true -> Set(false)
	// -> Set(true), so the newest audit entry should be from=false to=true.
	for _, e := range entries {
		if e.FlagKey == "vendor.enabled" {
			if e.FromEnabled != false || e.ToEnabled != true {
				t.Errorf("newest audit entry should be from=false to=true (post-flip); got from=%v to=%v", e.FromEnabled, e.ToEnabled)
			}
			break
		}
	}

	t.Cleanup(func() {
		ctx, _ := tenancy.WithTenant(context.Background(), tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}

// TestStoreSetSpineForbiddenRejected — Set refuses a spine-forbidden key.
// AC-A1 defense-in-depth.
func TestStoreSetSpineForbiddenRejected(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, _ := withTenant(t)

	_, err := store.Set(ctx, "rls.policies", false, "attacker", "")
	if err == nil {
		t.Fatalf("Set on spine-forbidden key returned nil error")
	}
	// Note: the canonical path is ErrNotFound (the key is not in Seed)
	// AND the spine-forbidden check fires next if the key WERE in Seed.
	// Either rejection is acceptable -- both are P0 anti-criteria.
}

// TestStoreSetUnknownKeyRejected — Set refuses a key absent from Seed.
// Defense against typo'd toggle attempts. ISC-29.
func TestStoreSetUnknownKeyRejected(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, _ := withTenant(t)

	_, err := store.Set(ctx, "completely.unknown.key", true, "anyone", "")
	if err == nil {
		t.Fatalf("Set on unknown key returned nil error; want ErrNotFound")
	}
}

// TestRLSCrossTenantIsolation — a flag toggled under tenant A is not
// visible under tenant B. Invariant 6 enforcement.
func TestRLSCrossTenantIsolation(t *testing.T) {
	store := featureflag.NewStore(appPool)

	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	ctxA, _ := tenancy.WithTenant(context.Background(), tenantA)
	ctxB, _ := tenancy.WithTenant(context.Background(), tenantB)

	// Toggle under A.
	if _, err := store.Set(ctxA, "policy.enabled", false, "alice", ""); err != nil {
		t.Fatalf("Set under tenant A: %v", err)
	}

	// Read under B -- should see seed default (true), not A's override.
	flag, err := store.Get(ctxB, "policy.enabled")
	if err != nil {
		t.Fatalf("Get under tenant B: %v", err)
	}
	if !flag.Enabled {
		t.Errorf("tenant B sees Enabled=false; want seed default true (RLS cross-tenant leak)")
	}
	if flag.HasOverride {
		t.Errorf("tenant B sees HasOverride=true; want false (RLS leak)")
	}

	t.Cleanup(func() {
		ctxA, _ := tenancy.WithTenant(context.Background(), tenantA)
		_, _ = appPool.Exec(ctxA, "DELETE FROM feature_flags WHERE tenant_id = $1", tenantA)
		_, _ = appPool.Exec(ctxA, "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenantA)
	})
}

// TestStoreListReturnsAllSeedDefaults — List on an empty tenant returns
// one Flag per Seed entry (no overrides). ISC-32.
func TestStoreListReturnsAllSeedDefaults(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, _ := withTenant(t)

	flags, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := len(flags), len(featureflag.Seed); got != want {
		t.Errorf("List returned %d flags; want %d (Seed length)", got, want)
	}
	for _, f := range flags {
		if f.HasOverride {
			t.Errorf("flag %q HasOverride=true on empty tenant", f.Key)
		}
	}
}

// TestStoreListMergesOverrides — toggling one flag and listing returns
// the override merged with the rest of the Seed defaults.
func TestStoreListMergesOverrides(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, tenant := withTenant(t)

	if _, err := store.Set(ctx, "audit.workflow", false, "alice", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	flags, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(flags) != len(featureflag.Seed) {
		t.Errorf("len(flags) = %d; want %d", len(flags), len(featureflag.Seed))
	}
	var saw bool
	for _, f := range flags {
		if f.Key == "audit.workflow" {
			saw = true
			if f.Enabled {
				t.Errorf("audit.workflow Enabled = true; want false")
			}
			if !f.HasOverride {
				t.Errorf("audit.workflow HasOverride = false; want true")
			}
			if f.LastChangedBy != "alice" {
				t.Errorf("LastChangedBy = %q; want alice", f.LastChangedBy)
			}
		}
	}
	if !saw {
		t.Errorf("audit.workflow not returned by List")
	}

	t.Cleanup(func() {
		ctx, _ := tenancy.WithTenant(context.Background(), tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}

// TestEnabledHelperEndToEnd — package-level Enabled() returns seed
// default first, then override after Set. AC-4 + ISC-19.
func TestEnabledHelperEndToEnd(t *testing.T) {
	store := featureflag.NewStore(appPool)
	ctx, tenant := withTenant(t)
	ctx = featureflag.WithCache(ctx)

	on, err := featureflag.Enabled(ctx, store, "exceptions.enabled")
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	if !on {
		t.Errorf("Enabled returned false; want seed default true")
	}

	// Second call in same context -- cached, no DB hit.
	on, err = featureflag.Enabled(ctx, store, "exceptions.enabled")
	if err != nil {
		t.Fatalf("Enabled (cached): %v", err)
	}
	if !on {
		t.Errorf("cached Enabled returned false; want true")
	}

	// Fresh context (no cache, fresh lookup) after Set(false) -- should
	// observe the override.
	if _, err := store.Set(ctx, "exceptions.enabled", false, "alice", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	freshCtx, _ := tenancy.WithTenant(context.Background(), tenant)
	freshCtx = featureflag.WithCache(freshCtx)
	on, err = featureflag.Enabled(freshCtx, store, "exceptions.enabled")
	if err != nil {
		t.Fatalf("Enabled (post-set): %v", err)
	}
	if on {
		t.Errorf("Enabled after Set(false) returned true; want false")
	}

	t.Cleanup(func() {
		ctx, _ := tenancy.WithTenant(context.Background(), tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(ctx, "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}
