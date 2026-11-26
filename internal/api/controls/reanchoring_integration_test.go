//go:build integration

// #2176 — Postgres-backed test for GET /v1/controls/needs-reanchoring.
//
// What this pins, against the non-owner app role so RLS binds:
//   - Only the #1971 D3 flagged control is listed, with its note. A control
//     on a legacy anchor whose code keeps its title (rebindable) and a
//     bundle pinned to its release are left out.
//   - Tenant isolation: another tenant's flagged control is not listed.
//   - Re-anchoring clears the warning: re-uploading the bundle on a
//     current-release anchor supersedes the flagged row, and the list is
//     empty again.
//
// Not t.Parallel: it adds a legacy SCF release next to the shared current
// one and removes it on cleanup.

package controls_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	apicontrols "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type reanchoringBody struct {
	Controls []struct {
		ID             string `json:"id"`
		BundleID       string `json:"bundle_id"`
		LegacySCFID    string `json:"legacy_scf_id"`
		LegacyRelease  string `json:"legacy_release"`
		CurrentRelease string `json:"current_release"`
		Note           string `json:"note"`
	} `json:"controls"`
	Count int `json:"count"`
}

func callNeedsReanchoring(t *testing.T, pool *pgxpool.Pool, tenantID string) reanchoringBody {
	t.Helper()
	h := apicontrols.NewReanchoringHandler(control.NewStore(pool))
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/needs-reanchoring", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{ID: "key_test_2176", TenantID: tenantID})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rr := httptest.NewRecorder()
	h.List(rr, req.WithContext(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out reanchoringBody
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestNeedsReanchoring_ListsFlaggedControlsAndClearsOnReanchor(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()

	const kept, gone, pinned = "TST-2176A", "TST-2176B", "TST-2176C"
	currentKept := insertAnchor(t, admin, kept, "TST")
	var frameworkID, currentVersion uuid.UUID
	var currentRelease string
	if err := admin.QueryRow(ctx, `
		SELECT fv.framework_id, fv.id, fv.version FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE a.id = $1`, currentKept).Scan(&frameworkID, &currentVersion, &currentRelease); err != nil {
		t.Fatalf("read current release: %v", err)
	}

	// A legacy release beside the current one. Registered before the
	// tenants, so it is removed after their controls (t.Cleanup is LIFO).
	legacyVersion := uuid.New()
	const legacyRelease = "test-legacy-2176"
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM controls WHERE scf_anchor_id IN (SELECT id FROM scf_anchors WHERE framework_version_id = $1)`,
			`DELETE FROM scf_anchors WHERE framework_version_id = $1`,
			`DELETE FROM framework_versions WHERE id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, legacyVersion); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, $3, 'legacy')`, legacyVersion, frameworkID, legacyRelease); err != nil {
		t.Fatalf("insert legacy release: %v", err)
	}
	legacy := map[string]uuid.UUID{}
	for _, code := range []string{kept, gone, pinned} {
		id := uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
			VALUES ($1, $2, $3, 'TST', 'Test anchor ' || $3)`, id, legacyVersion, code); err != nil {
			t.Fatalf("insert legacy anchor %s: %v", code, err)
		}
		legacy[code] = id
	}

	tenant := dbtest.SeedTenant(t, admin, "controls")
	other := dbtest.SeedTenant(t, admin, "controls")
	insertControl(t, admin, tenant, legacy[kept], "Same meaning", "TST", "reanchor_2176_kept")
	flagged := insertControl(t, admin, tenant, legacy[gone], "Code gone", "TST", "reanchor_2176_gone")
	pinnedID := insertControl(t, admin, tenant, legacy[pinned], "Pinned", "TST", "reanchor_2176_pinned")
	if _, err := admin.Exec(ctx, `UPDATE controls SET bundle_manifest_yaml = $2 WHERE id = $1`,
		pinnedID, "scf_anchor_id: "+pinned+"\nscf_release: "+legacyRelease+"\n"); err != nil {
		t.Fatalf("pin control: %v", err)
	}
	otherFlagged := insertControl(t, admin, other, legacy[gone], "Other tenant", "TST", "reanchor_2176_other")

	got := callNeedsReanchoring(t, app, tenant)
	if got.Count != 1 || len(got.Controls) != 1 {
		t.Fatalf("needs-reanchoring = %+v; want only the control whose code is gone", got)
	}
	c := got.Controls[0]
	if c.ID != flagged.String() || c.LegacySCFID != gone || c.LegacyRelease != legacyRelease || c.CurrentRelease != currentRelease {
		t.Errorf("listed %+v; want control %s on %s@%s", c, flagged, gone, legacyRelease)
	}
	if want := gone + " is not in " + currentRelease; c.Note != want {
		t.Errorf("note = %q; want %q", c.Note, want)
	}
	if o := callNeedsReanchoring(t, app, other); o.Count != 1 || o.Controls[0].ID != otherFlagged.String() {
		t.Fatalf("other tenant's list = %+v; want only its own control", o)
	}

	// Re-anchor: the operator re-uploads the bundle on a current-release
	// anchor. The flagged row is superseded and the warning clears.
	tctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	b, err := control.FinalizeBundleForHTTP([]byte(`bundle_schema_version: "1"
bundle_id: reanchor_2176_gone
title: "Code gone"
scf_anchor_id: ` + kept + `
scf_release: ` + currentRelease + `
implementation_type: automated
`))
	if err != nil {
		t.Fatalf("FinalizeBundleForHTTP: %v", err)
	}
	res, err := control.NewStore(app).Upload(tctx, b, "key_admin")
	if err != nil {
		t.Fatalf("re-anchoring upload: %v", err)
	}
	if res.ControlID == flagged {
		t.Fatalf("re-anchoring upload kept control %s; want a new version", flagged)
	}
	if after := callNeedsReanchoring(t, app, tenant); after.Count != 0 || len(after.Controls) != 0 {
		t.Fatalf("after re-anchoring = %+v; want an empty list", after)
	}
	if o := callNeedsReanchoring(t, app, other); o.Count != 1 {
		t.Fatalf("other tenant's list after tenant re-anchored = %+v; want it unchanged", o)
	}
}
