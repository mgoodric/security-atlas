//go:build integration

// Integration tests for slice 009: control bundle upload, version-stamping,
// supersession, and SCF-anchor enforcement. Real Postgres only —
// memory rule: "Never mock the DB".

package control_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// freshTenant returns a brand-new tenant id and registers a cleanup that
// wipes every row written under it. Each test owns its own tenant so RLS
// guarantees isolation.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		// Delete child rows first, then the controls. The previous
		// `UPDATE controls SET superseded_by = NULL` pre-step is removed:
		// it tried to NULL EVERY version of a bundle at once, which makes
		// two rows share (tenant_id, bundle_id) with superseded_by IS NULL
		// and trips the controls_one_active_version_per_bundle unique
		// index (23505). It is also unnecessary —
		// `controls_superseded_by_fk` is ON DELETE SET NULL, so deleting
		// the whole tenant's controls in one statement resolves the
		// self-reference on its own.
		for _, stmt := range []string{
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedSCFAnchor inserts a single SCF anchor row directly (no slice-006
// importer needed). Returns the anchor's uuid and its SCF code so tests can
// reference either.
func seedSCFAnchor(t *testing.T, admin *pgxpool.Pool, code, family string) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()

	// Ensure we have a scf framework + version. Idempotent.
	var frameworkID uuid.UUID
	err := admin.QueryRow(ctx, `
		SELECT id FROM frameworks WHERE slug = 'scf' AND tenant_id IS NULL
	`).Scan(&frameworkID)
	if errors.Is(err, pgx.ErrNoRows) {
		frameworkID = uuid.New()
		// frameworks columns: (id, tenant_id, name, slug, issuer,
		// description, ...). `issuer` is NOT NULL; there is no `source`
		// column. (This helper had bit-rotted against a pre-slice-002
		// schema because internal/control was never wired into the CI
		// integration job — slice 068 wires it in.)
		if _, err := admin.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
			VALUES ($1, NULL, 'scf', 'Secure Controls Framework', 'Secure Controls Framework Council')
		`, frameworkID); err != nil {
			t.Fatalf("insert framework: %v", err)
		}
	} else if err != nil {
		t.Fatalf("lookup framework: %v", err)
	}

	var versionID uuid.UUID
	err = admin.QueryRow(ctx, `
		SELECT id FROM framework_versions
		WHERE framework_id = $1 AND status = 'current'
	`, frameworkID).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		versionID = uuid.New()
		// framework_versions columns: (id, tenant_id, framework_id,
		// version, ..., status, ...). The version column is `version`,
		// not `release_version`; there is no `source` column.
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_versions
				(id, tenant_id, framework_id, version, status)
			VALUES ($1, NULL, $2, 'test-1.0', 'current')
		`, versionID, frameworkID); err != nil {
			t.Fatalf("insert framework_version: %v", err)
		}
	} else if err != nil {
		t.Fatalf("lookup framework_version: %v", err)
	}

	// Anchor itself.
	id := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (framework_version_id, scf_id) DO NOTHING
	`, id, versionID, code, family, "Test anchor "+code); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
	// Re-fetch in case ON CONFLICT skipped.
	if err := admin.QueryRow(ctx, `
		SELECT id FROM scf_anchors WHERE framework_version_id = $1 AND scf_id = $2
	`, versionID, code).Scan(&id); err != nil {
		t.Fatalf("fetch anchor: %v", err)
	}
	return id, code
}

// TestUpload_HappyPath_CreatesActiveRow — AC-3: posting a bundle creates a
// controls row tied to the SCF anchor, with version=1 and superseded_by NULL.
func TestUpload_HappyPath_CreatesActiveRow(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	_, code := seedSCFAnchor(t, admin, "IAC-06", "IAC")
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	bundle, err := control.FinalizeBundleForHTTP([]byte(yamlFor("happy_control", code, "automated")))
	if err != nil {
		t.Fatalf("FinalizeBundleForHTTP: %v", err)
	}

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	res, err := store.Upload(ctx, bundle, "key_admin")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Version != 1 || !res.IsNewBundle {
		t.Fatalf("expected initial upload; got version=%d isNew=%v", res.Version, res.IsNewBundle)
	}

	// Cross-check the row directly.
	var version int32
	var superseded *uuid.UUID
	if err := admin.QueryRow(context.Background(), `
		SELECT version, superseded_by FROM controls WHERE id = $1
	`, res.ControlID).Scan(&version, &superseded); err != nil {
		t.Fatalf("verify row: %v", err)
	}
	if version != 1 || superseded != nil {
		t.Fatalf("expected version=1 superseded=nil; got %d %v", version, superseded)
	}
}

// TestUpload_ReuploadSupersedes — AC-6: same bundle_id again bumps version,
// flags the predecessor's superseded_by.
func TestUpload_ReuploadSupersedes(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	_, code := seedSCFAnchor(t, admin, "IAC-07", "IAC")
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	b1, _ := control.FinalizeBundleForHTTP([]byte(yamlFor("supersede_test", code, "automated")))
	res1, err := store.Upload(ctx, b1, "key_admin")
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}

	b2, _ := control.FinalizeBundleForHTTP([]byte(yamlFor("supersede_test", code, "semi_automated")))
	res2, err := store.Upload(ctx, b2, "key_admin")
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if res2.IsNewBundle {
		t.Fatalf("second upload should not be IsNewBundle")
	}
	if res2.Version != 2 {
		t.Fatalf("expected version=2; got %d", res2.Version)
	}
	if res2.SupersededID != res1.ControlID {
		t.Fatalf("supersededID mismatch: got %s, want %s", res2.SupersededID, res1.ControlID)
	}

	// Cross-check: exactly one active row per bundle_id (partial unique
	// index invariant).
	var activeCount int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM controls
		WHERE tenant_id = $1 AND bundle_id = $2 AND superseded_by IS NULL
	`, tenant, "supersede_test").Scan(&activeCount); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly 1 active row; got %d", activeCount)
	}
}

// TestUpload_UnknownAnchor — AC-4 + invariant 7: bundle referencing an SCF
// anchor that isn't registered must be rejected with ErrSCFAnchorUnknown.
func TestUpload_UnknownAnchor(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	bundle, _ := control.FinalizeBundleForHTTP([]byte(yamlFor("ghost_control", "NEVER-99", "automated")))

	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	_, err := store.Upload(ctx, bundle, "key_admin")
	if err == nil {
		t.Fatalf("expected rejection")
	}
	if !errors.Is(err, control.ErrSCFAnchorUnknown) {
		t.Fatalf("expected ErrSCFAnchorUnknown; got %v", err)
	}
}

// yamlFor builds a minimal manifest body for a given bundle id, anchor, and
// implementation_type. Lets tests vary one axis at a time.
func yamlFor(bundleID, anchor, impl string) string {
	return `bundle_schema_version: "1"
bundle_id: ` + bundleID + `
title: "Test control"
scf_anchor_id: ` + anchor + `
implementation_type: ` + impl + `
`
}
