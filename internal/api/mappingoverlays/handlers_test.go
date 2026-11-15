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
