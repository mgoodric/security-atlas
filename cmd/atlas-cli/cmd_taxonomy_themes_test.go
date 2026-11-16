package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runThemes(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	resetControlsAuth(t)
	return runImportCmd(t, append([]string{"--endpoint", srv.URL, "--token", "tok", "taxonomy", "themes"}, args...)...)
}

func themeResp(name string) map[string]any {
	return map[string]any{"theme": map[string]any{"id": "t-1", "name": name, "source": "tenant"}}
}

func TestTaxonomyThemesList_PrintsSource(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, map[string]any{"themes": []map[string]any{
		{"name": "cloud-drift", "description": "Config drift", "source": "tenant"},
		{"name": "ownership", "description": "No owner", "source": "default"},
	}})

	out, err := runThemes(t, srv, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if api.method != http.MethodGet || api.path != "/v1/themes" || api.auth != "Bearer tok" {
		t.Errorf("request %s %s auth=%q", api.method, api.path, api.auth)
	}
	for _, want := range []string{
		"NAME", "SOURCE",
		"cloud-drift  organisation  Config drift",
		"ownership    built-in      No owner",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestTaxonomyThemesAdd_SendsNameDescription(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusCreated, themeResp("cloud-drift"))

	out, err := runThemes(t, srv, "add", "cloud-drift", "--description", "Config drift")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if api.method != http.MethodPost || api.path != "/v1/themes" ||
		api.body["name"] != "cloud-drift" || api.body["description"] != "Config drift" {
		t.Errorf("request %s %s body=%v", api.method, api.path, api.body)
	}
	if !strings.Contains(out, "added cloud-drift") {
		t.Errorf("output = %s", out)
	}
	if _, err := runThemes(t, srv, "add"); err == nil {
		t.Error("add without a name succeeded")
	}
}

func TestTaxonomyThemesAdd_ClashSurfacesConflict(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusConflict, map[string]any{
		"error": "risk: theme name already in use by a built-in or organisation theme",
	})
	_, err := runThemes(t, srv, "add", "ownership")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("err = %v, want the 409", err)
	}
}

func TestTaxonomyThemesRename_SendsOnlyChangedFieldsAndPrintsCounts(t *testing.T) {
	api := &taxonomyAPI{}
	resp := themeResp("cloud-config")
	resp["risks_updated"], resp["rules_updated"] = 4, 1
	srv := api.server(t, http.StatusOK, resp)

	out, err := runThemes(t, srv, "rename", "cloud-drift", "--name", "cloud-config")
	if err != nil {
		t.Fatalf("rename: %v\n%s", err, out)
	}
	if api.method != http.MethodPatch || api.path != "/v1/themes/cloud-drift" {
		t.Errorf("request %s %s", api.method, api.path)
	}
	if _, has := api.body["description"]; has || api.body["name"] != "cloud-config" {
		t.Errorf("body = %v, want name only", api.body)
	}
	for _, want := range []string{"renamed cloud-config", "updated 4 risk(s) and 1 aggregation rule(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := runThemes(t, srv, "rename", "cloud-drift"); err == nil {
		t.Error("rename with no flags succeeded")
	}
}

func TestTaxonomyThemesDelete(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusNoContent, nil)
	out, err := runThemes(t, srv, "delete", "cloud-drift")
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	if api.method != http.MethodDelete || api.path != "/v1/themes/cloud-drift" {
		t.Errorf("request %s %s", api.method, api.path)
	}
	if !strings.Contains(out, "deleted cloud-drift") {
		t.Errorf("output = %s", out)
	}
}

func TestTaxonomyThemesDelete_InUseSurfacesCounts(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusConflict, map[string]any{
		"error":      `risk: theme "cloud-drift" is used by 2 risk(s) and 1 aggregation rule(s)`,
		"risk_count": 2, "rule_count": 1,
	})
	_, err := runThemes(t, srv, "delete", "cloud-drift")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "2 risk(s) and 1 aggregation rule(s)") {
		t.Fatalf("err = %v, want the 409 with counts", err)
	}
}
