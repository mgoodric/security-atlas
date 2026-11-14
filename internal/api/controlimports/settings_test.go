package controlimports

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/controlimport"
)

type memSettings struct {
	s  controlimport.Settings
	by string
}

func (m *memSettings) GetSettings(context.Context) (controlimport.Settings, error) { return m.s, nil }

func (m *memSettings) SetSettings(_ context.Context, in controlimport.Settings, by string) (controlimport.Settings, error) {
	m.s, m.by = in, by
	return in, nil
}

func settingsRequest(t *testing.T, h *Handler, method, body string, cred *credstore.Credential) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	h.RegisterRoutes(r)
	req := httptest.NewRequest(method, "/v1/controls/imports/settings", strings.NewReader(body))
	if cred != nil {
		req = req.WithContext(authctx.WithCredential(req.Context(), *cred))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSettingsRequireTenantAdmin(t *testing.T) {
	t.Parallel()
	store := &memSettings{}
	h := New(nil).WithSettings(store)
	engineer := &credstore.Credential{ID: "eng", OwnerRoles: []string{"grc_engineer"}}
	admin := &credstore.Credential{ID: "adm", OwnerRoles: []string{"admin"}}

	cases := []struct {
		name   string
		method string
		body   string
		cred   *credstore.Credential
		want   int
	}{
		{"no credential", http.MethodGet, "", nil, http.StatusUnauthorized},
		{"engineer reads", http.MethodGet, "", engineer, http.StatusForbidden},
		{"engineer writes", http.MethodPut, `{"llm_rerank_local":true}`, engineer, http.StatusForbidden},
		{"admin, missing field", http.MethodPut, `{}`, admin, http.StatusBadRequest},
		{"admin, unknown field", http.MethodPut, `{"llm_rerank_local":true,"provider":"x"}`, admin, http.StatusBadRequest},
		{"admin writes", http.MethodPut, `{"llm_rerank_local":true}`, admin, http.StatusOK},
		{"admin reads", http.MethodGet, "", admin, http.StatusOK},
	}
	for _, tc := range cases {
		w := settingsRequest(t, h, tc.method, tc.body, tc.cred)
		if w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
	if !store.s.LLMRerankLocal || store.by != "adm" {
		t.Errorf("stored = %+v by %q, want the admin's opt-in", store.s, store.by)
	}
	w := settingsRequest(t, h, http.MethodGet, "", admin)
	var got controlimport.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.LLMRerankLocal {
		t.Errorf("GET body = %s, want llm_rerank_local true", w.Body.String())
	}
}

func TestSettingsRoutesAbsentWithoutAStore(t *testing.T) {
	t.Parallel()
	admin := &credstore.Credential{ID: "adm", IsAdmin: true}
	if w := settingsRequest(t, New(nil), http.MethodPut, `{"llm_rerank_local":true}`, admin); w.Code != http.StatusMethodNotAllowed && w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want the route unregistered", w.Code)
	}
}
