//go:build integration

// Issue #2392 — the legacy-anchor flag on `GET /v1/controls?include=state`.
//
// A control is flagged while it has an undecided SCF remap proposal: open,
// or rejected (which a reviewer can revisit). Approved, kept-legacy and
// rebound proposals clear it. ?legacy_anchor=true
// narrows the page and the total to the flagged controls. Another tenant's
// proposals never flag this tenant's rows.

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

// insertRemapProposal records a proposal for controlID in the given status.
// A rebound or approved one names target as the anchor it moved to.
func insertRemapProposal(t *testing.T, admin *pgxpool.Pool, tenant string, controlID, legacy, target uuid.UUID, status string) {
	t.Helper()
	var targetAnchor *uuid.UUID
	if status == "rebound" || status == "approved" {
		targetAnchor = &target
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO scf_remap_proposals
			(tenant_id, control_id, bundle_id, legacy_anchor_id, legacy_scf_id,
			 legacy_release, target_framework_version_id, target_release, class,
			 status, successor_scf_ids, target_anchor_id, source)
		SELECT c.tenant_id, c.id, c.bundle_id, $3, 'IAC-37', 'test-old',
		       a.framework_version_id, 'test-new', 'unchanged', $4, ARRAY['IAC-37.1'], $5, 'upload'
		FROM controls c, scf_anchors a
		WHERE c.tenant_id = $1 AND c.id = $2 AND a.id = $6
	`, tenant, controlID, legacy, status, targetAnchor, target); err != nil {
		t.Fatalf("insert %s proposal: %v", status, err)
	}
}

func TestListWithState_LegacyAnchorFlag(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_proposals", "controls")
	other := dbtest.SeedTenant(t, admin, "scf_remap_proposals", "controls")
	legacy := insertAnchor(t, admin, "IAC-37", "Identity & Access Mgmt")
	target := insertAnchor(t, admin, "IAC-37.1", "Identity & Access Mgmt")

	byStatus := map[string]uuid.UUID{}
	for _, s := range []string{"open", "rejected", "approved", "kept_legacy", "rebound"} {
		id := insertControl(t, admin, tenant, legacy, "on "+s, "IAC", "ctrl-2392-"+s+"-"+uuid.NewString()[:8])
		insertRemapProposal(t, admin, tenant, id, legacy, target, s)
		byStatus[s] = id
	}
	plain := insertControl(t, admin, tenant, legacy, "no proposal", "IAC", "ctrl-2392-plain-"+uuid.NewString()[:8])
	// Another tenant's open proposal on its own control flags nothing here.
	foreign := insertControl(t, admin, other, legacy, "other tenant", "IAC", "ctrl-2392-other-"+uuid.NewString()[:8])
	insertRemapProposal(t, admin, other, foreign, legacy, target, "open")

	all := callLegacyList(t, app, tenant, "include=state&limit=50")
	if all.Total != 6 {
		t.Fatalf("total = %d, want 6", all.Total)
	}
	want := map[uuid.UUID]bool{byStatus["open"]: true, byStatus["rejected"]: true}
	for _, c := range all.Controls {
		id := uuid.MustParse(c.ID)
		if c.LegacyAnchor != want[id] {
			t.Fatalf("control %s legacy_anchor = %v, want %v", c.ID, c.LegacyAnchor, want[id])
		}
		if id == foreign {
			t.Fatalf("listed another tenant's control %s", c.ID)
		}
	}
	if !containsControl(all, plain) {
		t.Fatalf("control without a proposal missing from the list")
	}

	flagged := callLegacyList(t, app, tenant, "include=state&legacy_anchor=true")
	if flagged.Total != 2 || flagged.Count != 2 {
		t.Fatalf("legacy_anchor=true: total %d count %d; want 2, 2", flagged.Total, flagged.Count)
	}
	for _, c := range flagged.Controls {
		if !want[uuid.MustParse(c.ID)] || !c.LegacyAnchor {
			t.Fatalf("legacy_anchor=true listed %s (flag %v)", c.ID, c.LegacyAnchor)
		}
	}
	if got := callLegacyList(t, app, tenant, "include=state&legacy_anchor=false"); got.Total != 6 {
		t.Fatalf("legacy_anchor=false: total %d, want 6", got.Total)
	}

	// Deciding the open proposal clears the flag.
	if _, err := admin.Exec(context.Background(),
		`UPDATE scf_remap_proposals SET status = 'kept_legacy' WHERE control_id = $1`, byStatus["open"]); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got := callLegacyList(t, app, tenant, "include=state&legacy_anchor=true"); got.Total != 1 || got.Controls[0].ID != byStatus["rejected"].String() {
		t.Fatalf("after keep-legacy: %+v; want only the rejected control", got)
	}
}

func TestListWithState_LegacyAnchorRejectsBadValue(t *testing.T) {
	t.Parallel()
	app := dbtest.NewAppPool(t)
	tenant := uuid.NewString()
	h := apicontrols.NewListHandler(control.NewStore(app))
	rr := httptest.NewRecorder()
	h.List(rr, legacyListRequest(t, tenant, "include=state&legacy_anchor=yes"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

type legacyListResp struct {
	Controls []struct {
		ID           string `json:"id"`
		LegacyAnchor bool   `json:"legacy_anchor"`
	} `json:"controls"`
	Count int   `json:"count"`
	Total int64 `json:"total"`
}

func containsControl(r legacyListResp, id uuid.UUID) bool {
	for _, c := range r.Controls {
		if c.ID == id.String() {
			return true
		}
	}
	return false
}

func legacyListRequest(t *testing.T, tenant, query string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/controls?"+query, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{ID: "key_test_2392", TenantID: tenant})
	ctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return req.WithContext(ctx)
}

func callLegacyList(t *testing.T, pool *pgxpool.Pool, tenant, query string) legacyListResp {
	t.Helper()
	h := apicontrols.NewListHandler(control.NewStore(pool))
	rr := httptest.NewRecorder()
	h.List(rr, legacyListRequest(t, tenant, query))
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status %d body %q", query, rr.Code, rr.Body.String())
	}
	var out legacyListResp
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
