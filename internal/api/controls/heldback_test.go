package controls

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/control"
)

type fakeHeldBack struct {
	rows   []control.HeldBackStockControl
	err    error
	called bool
}

func (f *fakeHeldBack) ListHeldBackStockControls(context.Context) ([]control.HeldBackStockControl, error) {
	f.called = true
	return f.rows, f.err
}

func serveHeldBack(f *fakeHeldBack, cred *credstore.Credential) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	NewHeldBackHandler(f).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/controls/held-back", nil)
	if cred != nil {
		req = req.WithContext(authctx.WithCredential(req.Context(), *cred))
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// TestHeldBack_TenantAdminOnly: the handler leg of the #2358 gate. Only a
// tenant admin by its live role (or the admin flag) reads the list; a
// grc_engineer, viewer or a JWT whose role claim says admin is refused.
func TestHeldBack_TenantAdminOnly(t *testing.T) {
	t.Parallel()
	const tenant = "00000000-0000-0000-0000-000000002358"
	const user = "11111111-1111-1111-1111-111111112358"
	for _, c := range []struct {
		name string
		cred *credstore.Credential
		want int
	}{
		{"no credential", nil, http.StatusUnauthorized},
		{"live tenant admin", &credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: user, TenantRoles: []string{"admin"}}, http.StatusOK},
		{"admin flag", &credstore.Credential{ID: "key_a", TenantID: tenant, IsAdmin: true}, http.StatusOK},
		{"live grc_engineer", &credstore.Credential{ID: "jwt:g", TenantID: tenant, UserID: user, TenantRoles: []string{"grc_engineer"}}, http.StatusForbidden},
		{"live viewer", &credstore.Credential{ID: "jwt:v", TenantID: tenant, UserID: user, TenantRoles: []string{"viewer"}}, http.StatusForbidden},
		{"jwt role claim only", &credstore.Credential{ID: "jwt:c", TenantID: tenant, UserID: user, OwnerRoles: []string{"admin"}}, http.StatusForbidden},
	} {
		f := &fakeHeldBack{rows: []control.HeldBackStockControl{{BundleID: "soc2_cc8_1_change_management"}}}
		rr := serveHeldBack(f, c.cred)
		if rr.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rr.Code, c.want, rr.Body.String())
		}
		if f.called != (c.want == http.StatusOK) {
			t.Errorf("%s: store called=%v, want %v", c.name, f.called, c.want == http.StatusOK)
		}
	}
}

func TestHeldBack_ListBodyAndStoreError(t *testing.T) {
	t.Parallel()
	admin := &credstore.Credential{ID: "key_a", IsAdmin: true}
	f := &fakeHeldBack{rows: []control.HeldBackStockControl{{BundleID: "b1", ShippedUpdate: true}}}
	rr := serveHeldBack(f, admin)
	var body HeldBackResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(body.Controls) != 1 || body.Controls[0].BundleID != "b1" || !body.Controls[0].ShippedUpdate {
		t.Fatalf("body = %+v", body)
	}
	if rr := serveHeldBack(&fakeHeldBack{err: errors.New("boom")}, admin); rr.Code != http.StatusInternalServerError {
		t.Fatalf("store error: status %d, want 500", rr.Code)
	}
}
