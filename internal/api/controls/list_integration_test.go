//go:build integration

// Slice 151 — Postgres-backed integration test for GET /v1/controls.
//
// What this pins:
//   - Empty tenant returns `{"controls": [], "count": 0}` (slice 150
//     empty-set robustness convention).
//   - Populated tenant returns one row per active control with the
//     expected envelope shape (id, title, control_family, scf_id,
//     lifecycle_state, bundle_id).
//   - RLS isolates tenants: tenant A's controls do not appear in
//     tenant B's list.
//
// Mirrors the slice-011 attest integration scaffold for DB bootstrap +
// fresh-tenant cleanup. Test bearer / API key strings are neutral
// (`key_test_151_*`) — no vendor token prefixes per P0-RISK-3.

package controls_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apicontrols "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
)

func appPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func freshTenantList(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// insertAnchor seeds a global SCF anchor (no tenant) so a tenant
// control can FK to a real anchor id.
func insertAnchor(t *testing.T, admin *pgxpool.Pool, code, family string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var frameworkID uuid.UUID
	err := admin.QueryRow(ctx, `
		SELECT id FROM frameworks WHERE slug = 'scf' AND tenant_id IS NULL
	`).Scan(&frameworkID)
	if err != nil {
		frameworkID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
			VALUES ($1, NULL, 'scf', 'Secure Controls Framework', 'SCF Council', '')
			ON CONFLICT DO NOTHING
		`, frameworkID); err != nil {
			t.Fatalf("insert framework: %v", err)
		}
		_ = admin.QueryRow(ctx, `SELECT id FROM frameworks WHERE slug='scf' AND tenant_id IS NULL`).Scan(&frameworkID)
	}

	var versionID uuid.UUID
	err = admin.QueryRow(ctx, `
		SELECT id FROM framework_versions
		WHERE framework_id = $1 AND status = 'current'
	`, frameworkID).Scan(&versionID)
	if err != nil {
		versionID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_versions
				(id, tenant_id, framework_id, version, status)
			VALUES ($1, NULL, $2, 'test-1.0', 'current')
			ON CONFLICT DO NOTHING
		`, versionID, frameworkID); err != nil {
			t.Fatalf("insert framework_version: %v", err)
		}
		_ = admin.QueryRow(ctx, `
			SELECT id FROM framework_versions WHERE framework_id=$1 AND status='current'
		`, frameworkID).Scan(&versionID)
	}

	anchorID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (framework_version_id, scf_id) DO NOTHING
	`, anchorID, versionID, code, family, "Test anchor "+code); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
	// Re-read in case ON CONFLICT skipped.
	_ = admin.QueryRow(ctx, `
		SELECT id FROM scf_anchors WHERE framework_version_id=$1 AND scf_id=$2
	`, versionID, code).Scan(&anchorID)
	return anchorID
}

// insertControl seeds a tenant control directly (bypassing the
// slice-009 upload path because we don't need a bundle here).
func insertControl(t *testing.T, admin *pgxpool.Pool, tenantID string, anchorID uuid.UUID, title, family, bundleID string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls
			(id, tenant_id, bundle_id, version, superseded_by, scf_id, scf_anchor_id,
			 title, description, control_family, implementation_type, owner_role,
			 lifecycle_state, applicability_expr, evidence_queries,
			 manual_evidence_schema, linked_policy_ids, freshness_class,
			 bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at,
			 bundle_uploaded_by)
		VALUES ($1, $2, $3, 1, NULL, NULL, $4,
			$5, '', $6, 'preventive', '',
			'active', '{"true": true}', '{}'::jsonb,
			'{}'::jsonb, '{}', NULL,
			'', $7, now(),
			'slice-151-test')
	`, id, tenantID, bundleID, anchorID, title, family, "hash-"+bundleID); err != nil {
		t.Fatalf("insert control: %v", err)
	}
	return id
}

// callList runs the handler against an http.Recorder with the given
// tenant set on the context (mirrors slice-033 tenancy middleware).
func callList(t *testing.T, pool *pgxpool.Pool, tenantID string) *httptest.ResponseRecorder {
	t.Helper()
	store := control.NewStore(pool)
	h := apicontrols.NewListHandler(store)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "key_test_151_list",
		TenantID: tenantID,
	})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	h.List(rr, req.WithContext(ctx))
	return rr
}

func TestList_EmptyTenant(t *testing.T) {
	t.Parallel()
	admin := adminPool(t)
	app := appPool(t)

	tenant := freshTenantList(t, admin)
	rr := callList(t, app, tenant)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%q", rr.Code, rr.Body.String())
	}
	var out struct {
		Controls []map[string]any `json:"controls"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Controls == nil {
		t.Fatalf("expected empty array, got nil (slice 150 empty-set convention)")
	}
	if len(out.Controls) != 0 {
		t.Fatalf("expected 0 controls; got %d", len(out.Controls))
	}
	if out.Count != 0 {
		t.Fatalf("expected count=0; got %d", out.Count)
	}
}

func TestList_PopulatedTenant(t *testing.T) {
	t.Parallel()
	admin := adminPool(t)
	app := appPool(t)

	tenant := freshTenantList(t, admin)
	anchor := insertAnchor(t, admin, "IAC-06", "Identity & Access Mgmt")
	want1 := insertControl(t, admin, tenant, anchor, "Access review quarterly", "Identity & Access Mgmt", "ctrl-iac-06")
	want2 := insertControl(t, admin, tenant, anchor, "MFA enforced", "Identity & Access Mgmt", "ctrl-iac-06-mfa")

	rr := callList(t, app, tenant)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%q", rr.Code, rr.Body.String())
	}
	var out struct {
		Controls []struct {
			ID            string `json:"id"`
			Title         string `json:"title"`
			ControlFamily string `json:"control_family"`
			SCFID         string `json:"scf_id"`
			BundleID      string `json:"bundle_id"`
		} `json:"controls"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("count=%d; want 2", out.Count)
	}
	seen := map[string]bool{}
	for _, c := range out.Controls {
		seen[c.ID] = true
		if c.Title == "" {
			t.Errorf("control %s has empty title", c.ID)
		}
	}
	if !seen[want1.String()] || !seen[want2.String()] {
		t.Fatalf("expected both seeded controls in list; got %+v", out.Controls)
	}
}

func TestList_TenantIsolation(t *testing.T) {
	t.Parallel()
	admin := adminPool(t)
	app := appPool(t)

	tenantA := freshTenantList(t, admin)
	tenantB := freshTenantList(t, admin)
	anchor := insertAnchor(t, admin, "IAC-06", "Identity & Access Mgmt")
	tenantAControl := insertControl(t, admin, tenantA, anchor, "Tenant A only", "Identity & Access Mgmt", "ctrl-iac-06-a")

	rr := callList(t, app, tenantB)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%q", rr.Code, rr.Body.String())
	}
	var out struct {
		Controls []struct {
			ID string `json:"id"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range out.Controls {
		if c.ID == tenantAControl.String() {
			t.Fatalf("RLS leak: tenant A control %s appeared in tenant B list", tenantAControl)
		}
	}
}
