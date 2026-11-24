package evidence

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/evidence/historicalimport"
)

// #1742: a non-admin credential gets 403 before the body is read; the
// importer (nil here) is never reached, so nothing is written.
func TestImportHTTP_NonAdmin403(t *testing.T) {
	t.Parallel()
	h := NewImportHandler(nil)
	tenant := uuid.NewString()
	for _, cred := range []credstore.Credential{
		{ID: "key_conn", TenantID: tenant},
		{ID: "key_grc", TenantID: tenant, TenantRoles: []string{"grc_engineer"}},
		{ID: "system:x", TenantID: tenant, IsAdmin: true, System: true},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/evidence:import", strings.NewReader(`{"source":"s","records":[{}]}`))
		req = req.WithContext(authctx.WithCredential(req.Context(), cred))
		rec := httptest.NewRecorder()
		h.ImportHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403 (body %s)", cred.ID, rec.Code, rec.Body)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence:import", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ImportHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: status %d, want 401", rec.Code)
	}
}

func TestWriteImportError_Mapping(t *testing.T) {
	t.Parallel()
	pid := uuid.New()
	cases := []struct {
		err  error
		code int
		body string
	}{
		{historicalimport.ErrNotAdmin, http.StatusForbidden, "forbidden"},
		{historicalimport.ErrInvalid, http.StatusBadRequest, "invalid_request"},
		{&historicalimport.FrozenPeriodError{Hits: []historicalimport.FrozenHit{{PeriodID: pid, Name: "FY25", Records: 3}}}, http.StatusConflict, pid.String()},
		{&historicalimport.AmendTargetError{PeriodID: pid, Reason: "r"}, http.StatusBadRequest, "invalid_amend_frozen"},
		{errors.New("boom"), http.StatusInternalServerError, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		writeImportError(rec, httptest.NewRequest(http.MethodPost, "/v1/evidence:import", nil), c.err)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%v: %d %s, want %d containing %q", c.err, rec.Code, rec.Body, c.code, c.body)
		}
	}
}
