package oscalexport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The handler's DB-backed happy path is covered by the internal/oscal
// integration test (real Postgres + real Python bridge). These unit
// tests cover the request-parsing and error-mapping logic that needs no
// database.

func TestExport_RejectsNonUUIDPeriodID(t *testing.T) {
	h := New(nil) // exporter is never reached — the id parse fails first
	r := chi.NewRouter()
	r.Post("/v1/audit-periods/{id}/oscal-export", h.Export)

	req := httptest.NewRequest(http.MethodPost, "/v1/audit-periods/not-a-uuid/oscal-export", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-UUID period id", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UUID") {
		t.Errorf("body should explain the UUID requirement, got %q", rec.Body.String())
	}
}

func TestExport_RejectsMalformedJSONBody(t *testing.T) {
	h := New(nil)
	r := chi.NewRouter()
	r.Post("/v1/audit-periods/{id}/oscal-export", h.Export)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/audit-periods/11111111-1111-1111-1111-111111111111/oscal-export",
		strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed JSON body", rec.Code)
	}
}
