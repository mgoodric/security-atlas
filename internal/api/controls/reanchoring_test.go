package controls

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #2176 — handler-shape tests for GET /v1/controls/needs-reanchoring. The
// Postgres-backed path (RLS, tenant id, re-anchoring clears the row) is in
// reanchoring_integration_test.go.

type stubLegacyLister struct {
	rows []dbx.ListControlsOnLegacySCFAnchorsRow
	err  error
}

func (s stubLegacyLister) ListOnLegacySCFAnchors(context.Context) ([]dbx.ListControlsOnLegacySCFAnchorsRow, error) {
	return s.rows, s.err
}

func reanchoringRequest(t *testing.T, h *ReanchoringHandler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/needs-reanchoring", nil)
	req = req.WithContext(withAuthAndTenant(req.Context(), credstore.Credential{
		TenantID: "00000000-0000-0000-0000-0000000000a1",
		UserID:   "u1",
	}))
	rr := httptest.NewRecorder()
	h.List(rr, req)
	return rr
}

func TestReanchoring_RequiresTenantContext(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NewReanchoringHandler(stubLegacyLister{}).List(rr, httptest.NewRequest(http.MethodGet, "/v1/controls/needs-reanchoring", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rr.Code)
	}
}

func TestReanchoring_EmptyEnvelope(t *testing.T) {
	t.Parallel()
	rr := reanchoringRequest(t, NewReanchoringHandler(stubLegacyLister{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "{\"controls\":[],\"count\":0}\n" {
		t.Errorf("body = %q; want the empty envelope", got)
	}
}

func TestReanchoring_StoreErrorIs500(t *testing.T) {
	t.Parallel()
	rr := reanchoringRequest(t, NewReanchoringHandler(stubLegacyLister{err: errors.New("boom")}))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rr.Code)
	}
}

// Only the flagged control is listed: the rebindable one (same code, same
// title) and the pinned one (declared scf_release) are left out.
func TestReanchoring_ListsOnlyFlaggedControlsWithNotes(t *testing.T) {
	t.Parallel()
	pam := "Privileged Account Management (PAM)"
	policy := "Policy"
	current := pgtype.UUID{Bytes: [16]byte{9}, Valid: true}
	row := func(id byte, bundle, scfID, legacyTitle string, currentTitle *string, manifest string) dbx.ListControlsOnLegacySCFAnchorsRow {
		r := dbx.ListControlsOnLegacySCFAnchorsRow{
			ControlID:          pgtype.UUID{Bytes: [16]byte{id}, Valid: true},
			BundleID:           bundle,
			ControlTitle:       bundle + " title",
			BundleManifestYaml: manifest,
			LegacyScfID:        scfID,
			LegacyAnchorTitle:  legacyTitle,
			LegacyRelease:      "test-2026.1",
			CurrentRelease:     "test-2026.3",
		}
		if currentTitle != nil {
			r.CurrentAnchorID = current
			r.CurrentAnchorTitle = currentTitle
		}
		return r
	}
	h := NewReanchoringHandler(stubLegacyLister{rows: []dbx.ListControlsOnLegacySCFAnchorsRow{
		row(1, "rebindable", "IAC-01", "Policy", &policy, "scf_anchor_id: IAC-01\n"),
		row(2, "flagged", "IAC-21", "Privileged Account Management", nil, "scf_anchor_id: IAC-21\n"),
		row(3, "pinned", "IAC-10", "Authenticator Management", &pam, "scf_anchor_id: IAC-10\nscf_release: test-2026.1\n"),
	}})
	rr := reanchoringRequest(t, h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 body=%s", rr.Code, rr.Body.String())
	}
	var got reanchoringResp
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || len(got.Controls) != 1 {
		t.Fatalf("got %+v; want exactly the flagged control", got)
	}
	c := got.Controls[0]
	if c.BundleID != "flagged" || c.LegacySCFID != "IAC-21" || c.LegacyRelease != "test-2026.1" || c.CurrentRelease != "test-2026.3" {
		t.Errorf("control = %+v", c)
	}
	if c.Note != "IAC-21 -> IAC-10 in test-2026.3 (concept)" {
		t.Errorf("note = %q; want the remap-table note", c.Note)
	}
	if c.ID != "02000000-0000-0000-0000-000000000000" {
		t.Errorf("id = %q", c.ID)
	}
}
