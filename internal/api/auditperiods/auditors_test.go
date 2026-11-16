package auditperiods

// #2118: the period-auditor routes refuse before they reach the store. The
// store-backed paths (assign, remove, log rows, tenant checks) are pinned by
// internal/api/period_auditors_integration_test.go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const auditorsTestTenant = "11111111-2118-4118-8118-111111111111"

func serveAuditors(t *testing.T, cred *credstore.Credential, method, target, body string) int {
	t.Helper()
	h := NewAuditors(nil) // every case refuses before the store
	r := chi.NewRouter()
	r.Get("/v1/audit-periods/{id}/auditors", h.List)
	r.Post("/v1/audit-periods/{id}/auditors", h.Assign)
	r.Delete("/v1/audit-periods/{id}/auditors/{userID}", h.Remove)

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if cred != nil {
		ctx := authctx.WithCredential(req.Context(), *cred)
		ctx, err := tenancy.WithTenant(ctx, auditorsTestTenant)
		if err != nil {
			t.Fatalf("with tenant: %v", err)
		}
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestAuditors_RefusedBeforeTheStore(t *testing.T) {
	t.Parallel()
	const period = "/v1/audit-periods/22222222-2118-4118-8118-222222222222/auditors"
	admin := &credstore.Credential{ID: "k", UserID: "user:a", TenantID: auditorsTestTenant, OwnerRoles: []string{"admin"}}
	grc := &credstore.Credential{ID: "k", UserID: "user:g", TenantID: auditorsTestTenant, OwnerRoles: []string{"grc_engineer"}}
	aud := &credstore.Credential{ID: "k", UserID: "user:u", TenantID: auditorsTestTenant, OwnerRoles: []string{"auditor"}}

	cases := []struct {
		name   string
		cred   *credstore.Credential
		method string
		target string
		body   string
		want   int
	}{
		{"no credential", nil, http.MethodGet, period, "", http.StatusUnauthorized},
		{"grc_engineer assigns", grc, http.MethodPost, period, `{"user_id":"33333333-2118-4118-8118-333333333333"}`, http.StatusForbidden},
		{"auditor lists", aud, http.MethodGet, period, "", http.StatusForbidden},
		{"auditor removes", aud, http.MethodDelete, period + "/33333333-2118-4118-8118-333333333333", "", http.StatusForbidden},
		{"period id not a uuid", admin, http.MethodGet, "/v1/audit-periods/nope/auditors", "", http.StatusBadRequest},
		{"body not JSON", admin, http.MethodPost, period, `{`, http.StatusBadRequest},
		{"user_id not a uuid", admin, http.MethodPost, period, `{"user_id":"user:x"}`, http.StatusBadRequest},
		{"removed user not a uuid", admin, http.MethodDelete, period + "/nope", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := serveAuditors(t, tc.cred, tc.method, tc.target, tc.body); got != tc.want {
				t.Fatalf("%s %s = %d; want %d", tc.method, tc.target, got, tc.want)
			}
		})
	}
}
