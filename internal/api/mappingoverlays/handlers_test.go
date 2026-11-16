package mappingoverlays

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// Every refusal the store classifies has an HTTP status: a code missing
// from refusalStatus would be written as status 0.
func TestRefusalStatus_CoversEveryClassifiedCode(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		&mappingoverlay.InvalidEdgeError{Reason: "x"},
		mappingoverlay.ErrUnknownFrameworkVersion,
		mappingoverlay.ErrUnknownRequirement,
		scfrelease.ErrUnknownRelease,
		scfrelease.ErrCodeNotInRelease,
		&mappingoverlay.CatalogEdgeExistsError{EdgeID: uuid.New()},
		mappingoverlay.ErrDuplicateOverlay,
		mappingoverlay.ErrAlreadyRetired,
		mappingoverlay.ErrNotFound,
		&mappingoverlay.InvalidRemapError{Reason: "x"},
		mappingoverlay.ErrRemapNotFound,
		mappingoverlay.ErrRemapDecided,
		mappingoverlay.ErrRemapAnchorRequired,
		mappingoverlay.ErrRemapAnchorNotInRelease,
		mappingoverlay.ErrRemapEdgeChanged,
	} {
		code, _, ok := mappingoverlay.Classify(err)
		if !ok || refusalStatus[code] == 0 {
			t.Errorf("%v: code %q has no status", err, code)
		}
	}
	rec := httptest.NewRecorder()
	writeErr(rec, httptest.NewRequest(http.MethodGet, "/", nil), "op", errors.New("db down"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unclassified error: status %d", rec.Code)
	}
}

func TestImportFormat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		query, contentType, want string
		ok                       bool
	}{
		{"?format=csv", "", "csv", true},
		{"?format=yaml", "text/csv", "yaml", true},
		{"?format=xlsx", "", "", false},
		{"", "text/csv; charset=utf-8", "csv", true},
		{"", "application/yaml", "yaml", true},
		{"", "application/x-yaml", "yaml", true},
		{"", "application/json", "", false},
		{"", "", "", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/mapping-overlays/import"+tc.query, nil)
		if tc.contentType != "" {
			r.Header.Set("Content-Type", tc.contentType)
		}
		got, ok := importFormat(r)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("query %q type %q: got %q, %v; want %q, %v", tc.query, tc.contentType, got, ok, tc.want, tc.ok)
		}
	}
}

// The audited-under report refuses a request with no tenant before reaching
// the store (nil here, so a store call would panic).
func TestAuditedUnder_RequiresTenant(t *testing.T) {
	t.Parallel()
	h := New(nil)
	for path, serve := range map[string]http.HandlerFunc{
		"/v1/mapping-overlays/audited-under":        h.AuditedUnder,
		"/v1/mapping-overlays/audited-under/export": h.AuditedUnderExport,
	} {
		rec := httptest.NewRecorder()
		serve(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with no tenant: status %d; want 401", path, rec.Code)
		}
	}
}

// An unanchored control's row carries anchor_id null and no anchor code.
func TestGapWireFrom_Unanchored(t *testing.T) {
	t.Parallel()
	w := gapWireFrom(mappingoverlay.Gap{ControlID: uuid.New(), AnchorFit: "none", AnchorFitNote: "no SCF equivalent"})
	if w.AnchorID != nil || w.AnchorCode != "" {
		t.Errorf("anchor = %v %q; want none", w.AnchorID, w.AnchorCode)
	}
	if w.CandidateAnchors == nil {
		t.Error("candidate_anchors is nil; want an empty list so it encodes as []")
	}
}
