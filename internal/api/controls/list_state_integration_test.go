//go:build integration

// Issue #1741 — `GET /v1/controls?include=state`, the /controls page's
// default "My controls" view.
//
// What this pins:
//   - One row per active control of the caller's tenant, paged server-side
//     with an unpaged `total`.
//   - Tenant isolation: Tenant B's controls never appear in Tenant A's
//     pages or total.
//   - A control with no evaluation carries `state: null`; each row carries
//     its scf_anchor_id and a non-null frameworks array.

package controls_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	apicontrols "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
)

type controlsWithStateResp struct {
	Controls []struct {
		ID          string          `json:"id"`
		SCFAnchorID *string         `json:"scf_anchor_id"`
		State       *map[string]any `json:"state"`
		Frameworks  []string        `json:"frameworks"`
	} `json:"controls"`
	Count int   `json:"count"`
	Total int64 `json:"total"`
}

func callListWithState(t *testing.T, pool *pgxpool.Pool, tenantID, query string) controlsWithStateResp {
	t.Helper()
	h := apicontrols.NewListHandler(control.NewStore(pool))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/controls?"+query, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "key_test_1741_list",
		TenantID: tenantID,
	})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	h.List(rr, req.WithContext(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status %d body=%q", query, rr.Code, rr.Body.String())
	}
	var out controlsWithStateResp
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestListWithState_PagesTenantControlsOnly(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := dbtest.SeedTenant(t, admin, "controls")
	tenantB := dbtest.SeedTenant(t, admin, "controls")
	anchor := insertAnchor(t, admin, "IAC-37", "Identity & Access Mgmt")
	want := map[string]bool{
		insertControl(t, admin, tenantA, anchor, "A one", "IAC", "ctrl-1741-a1").String():   true,
		insertControl(t, admin, tenantA, anchor, "A two", "IAC", "ctrl-1741-a2").String():   true,
		insertControl(t, admin, tenantA, anchor, "A three", "IAC", "ctrl-1741-a3").String(): true,
	}
	other := insertControl(t, admin, tenantB, anchor, "B only", "IAC", "ctrl-1741-b1").String()

	first := callListWithState(t, app, tenantA, "include=state&limit=2")
	second := callListWithState(t, app, tenantA, "include=state&limit=2&offset=2")
	if first.Total != 3 || second.Total != 3 {
		t.Fatalf("totals = %d, %d; want 3", first.Total, second.Total)
	}
	if first.Count != 2 || second.Count != 1 {
		t.Fatalf("page counts = %d, %d; want 2 and 1", first.Count, second.Count)
	}

	seen := map[string]bool{}
	for _, c := range append(first.Controls, second.Controls...) {
		if c.ID == other {
			t.Fatalf("tenant A listed tenant B's control %s", other)
		}
		if !want[c.ID] {
			t.Fatalf("unexpected control %s", c.ID)
		}
		if seen[c.ID] {
			t.Fatalf("control %s on two pages", c.ID)
		}
		seen[c.ID] = true
		if c.State != nil {
			t.Fatalf("control %s has no evaluation; state = %+v", c.ID, *c.State)
		}
		if c.SCFAnchorID == nil || *c.SCFAnchorID != anchor.String() {
			t.Fatalf("control %s scf_anchor_id = %v; want %s", c.ID, c.SCFAnchorID, anchor)
		}
		if c.Frameworks == nil {
			t.Fatalf("control %s frameworks is null; want []", c.ID)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d controls across pages; want 3", len(seen))
	}

	b := callListWithState(t, app, tenantB, "include=state")
	if b.Total != 1 || len(b.Controls) != 1 || b.Controls[0].ID != other {
		t.Fatalf("tenant B = %+v; want only %s", b, other)
	}
}
