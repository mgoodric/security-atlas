//go:build integration

// #2170 — switching an optional module off end to end. Questionnaires is
// the tracer: an admin switches it off through PATCH /v1/admin/features,
// every questionnaires route answers 404 with the switched-off marker,
// the caller's effective modules report it off, and switching it back on
// restores the same data. Each request goes through the production router
// (HTTPHandlerForTests: JWT, tenancy, the module gate, OPA) on real
// Postgres under RLS.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type moduleGateFixture struct {
	url           string
	admin, viewer string
}

func newModuleGateFixture(t *testing.T) *moduleGateFixture {
	t.Helper()
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate,
		"questionnaire_answers", "questionnaire_questions", "questionnaires",
		"feature_flag_audit_log", "feature_flags", "decision_audit_log")
	tid := uuid.MustParse(tenant)

	adminClaims := testjwt.AdminFor(tid)
	viewerClaims := testjwt.ViewerFor(tid)
	engine, err := authz.NewEngine(context.Background(), noteSubjectRoles{
		adminClaims.Subject:  {authz.RoleAdmin},
		viewerClaims.Subject: {authz.RoleViewer},
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	f := &moduleGateFixture{
		admin:  srv.IssueTestJWT(t, adminClaims),
		viewer: srv.IssueTestJWT(t, viewerClaims),
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	f.url = ts.URL
	return f
}

type moduleGateResponse struct {
	status int
	marker string
	body   string
}

func (f *moduleGateFixture) do(t *testing.T, method, path, bearer string, body any) moduleGateResponse {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.url+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return moduleGateResponse{status: resp.StatusCode, marker: resp.Header.Get("X-Feature-Disabled"), body: string(b)}
}

func (f *moduleGateFixture) setSwitch(t *testing.T, key string, on bool) {
	t.Helper()
	got := f.do(t, http.MethodPatch, "/v1/admin/features/"+key, f.admin, map[string]any{"enabled": on, "reason": "#2170 test"})
	if got.status != http.StatusOK {
		t.Fatalf("PATCH %s enabled=%v: status %d body=%s", key, on, got.status, got.body)
	}
}

func (f *moduleGateFixture) createQuestionnaire(t *testing.T) string {
	t.Helper()
	got := f.do(t, http.MethodPost, "/v1/questionnaires", f.admin, map[string]any{"name": "#2170 vendor DDQ", "source_label": "test"})
	if got.status != http.StatusCreated && got.status != http.StatusOK {
		t.Fatalf("POST /v1/questionnaires: status %d body=%s", got.status, got.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(got.body), &created); err != nil || created.ID == "" {
		t.Fatalf("POST /v1/questionnaires: no id in %s (%v)", got.body, err)
	}
	return created.ID
}

// modules returns GET /v1/me/modules as key -> state.
func (f *moduleGateFixture) modules(t *testing.T, bearer string) map[string]string {
	t.Helper()
	got := f.do(t, http.MethodGet, "/v1/me/modules", bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/me/modules: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Modules []struct {
			Key   string   `json:"key"`
			State string   `json:"state"`
			Pages []string `json:"pages"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode /v1/me/modules: %v; body=%s", err, got.body)
	}
	out := map[string]string{}
	for _, m := range body.Modules {
		out[m.Key] = m.State
	}
	return out
}

func TestModuleGate_QuestionnairesSwitchedOff(t *testing.T) {
	f := newModuleGateFixture(t)
	id := f.createQuestionnaire(t)

	// The representative route and a nested one.
	routes := []string{"/v1/questionnaires", "/v1/questionnaires/" + id, "/v1/questionnaires/" + id + "/suggestions"}

	// Switched on (the default): the routes behave as today.
	for _, bearer := range []string{f.admin, f.viewer} {
		for _, path := range routes[:2] {
			if got := f.do(t, http.MethodGet, path, bearer, nil); got.status != http.StatusOK || got.marker != "" {
				t.Fatalf("on: GET %s: status %d marker %q body=%s; want 200, no marker", path, got.status, got.marker, got.body)
			}
		}
	}
	before := f.do(t, http.MethodGet, "/v1/questionnaires/"+id, f.admin, nil).body

	f.setSwitch(t, "questionnaires.enabled", false)
	for _, bearer := range []string{f.admin, f.viewer} {
		for _, path := range routes {
			got := f.do(t, http.MethodGet, path, bearer, nil)
			if got.status != http.StatusNotFound || got.marker != "questionnaires.enabled" {
				t.Fatalf("off: GET %s: status %d marker %q; want 404 with X-Feature-Disabled: questionnaires.enabled", path, got.status, got.marker)
			}
			if got.body != "{\"error\":\"feature disabled\"}\n" {
				t.Fatalf("off: GET %s: body %q; want the switched-off body", path, got.body)
			}
		}
	}
	// A write is refused too, before it reaches the handler.
	if got := f.do(t, http.MethodPost, "/v1/questionnaires", f.admin, map[string]any{"name": "refused"}); got.status != http.StatusNotFound || got.marker != "questionnaires.enabled" {
		t.Fatalf("off: POST /v1/questionnaires: status %d marker %q; want 404 with marker", got.status, got.marker)
	}
	// Core routes are unaffected.
	if got := f.do(t, http.MethodGet, "/v1/me", f.viewer, nil); got.status != http.StatusOK {
		t.Fatalf("off: GET /v1/me: status %d; a core route must not be gated", got.status)
	}

	// Switched back on: the same data, unchanged.
	f.setSwitch(t, "questionnaires.enabled", true)
	after := f.do(t, http.MethodGet, "/v1/questionnaires/"+id, f.admin, nil)
	if after.status != http.StatusOK {
		t.Fatalf("on again: GET questionnaire: status %d body=%s", after.status, after.body)
	}
	if after.body != before {
		t.Fatalf("questionnaire changed across off/on:\nbefore=%s\nafter =%s", before, after.body)
	}
	list := f.do(t, http.MethodGet, "/v1/questionnaires", f.viewer, nil)
	if list.status != http.StatusOK || !bytes.Contains([]byte(list.body), []byte(id)) {
		t.Fatalf("on again: list status %d does not include %s: %s", list.status, id, list.body)
	}
}

func TestModuleGate_EffectiveModules(t *testing.T) {
	f := newModuleGateFixture(t)

	// #2171: every optional module, each on by default except Board
	// reporting, so existing tenants keep today's behaviour.
	want := map[string]string{
		"risk": "on", "third_party_risk": "on", "policies": "on", "questionnaires": "on",
		"personnel_security": "on", "exceptions": "on", "board_reporting": "switched_off",
	}
	for name, bearer := range map[string]string{"admin": f.admin, "viewer": f.viewer} {
		if got := f.modules(t, bearer); !equalStates(got, want) {
			t.Fatalf("defaults, %s: %v; want %v", name, got, want)
		}
	}

	f.setSwitch(t, "questionnaires.enabled", false)
	f.setSwitch(t, "board.reporting", true)
	f.setSwitch(t, "vendor.claims", true)
	want["questionnaires"], want["board_reporting"] = "switched_off", "on"
	for name, bearer := range map[string]string{"admin": f.admin, "viewer": f.viewer} {
		if got := f.modules(t, bearer); !equalStates(got, want) {
			t.Fatalf("flipped, %s: %v; want %v", name, got, want)
		}
	}

	// Board reporting and Vendor claims keep their gate, now from the
	// registry: switched off again, their routes answer the marker.
	f.setSwitch(t, "board.reporting", false)
	f.setSwitch(t, "vendor.claims", false)
	for path, key := range map[string]string{
		"/v1/board-packs":                 "board.reporting",
		"/v1/board-briefs":                "board.reporting",
		"/v1/oscal/component-definitions": "vendor.claims",
	} {
		if got := f.do(t, http.MethodGet, path, f.admin, nil); got.status != http.StatusNotFound || got.marker != key {
			t.Fatalf("GET %s: status %d marker %q; want 404 marker %q", path, got.status, got.marker, key)
		}
	}
}

func equalStates(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
