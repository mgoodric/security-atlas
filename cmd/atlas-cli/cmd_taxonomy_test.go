package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// taxonomyAPI stands in for /v1/risks/categories and records the last
// request it received.
type taxonomyAPI struct {
	method, path, query, auth string
	body                      map[string]any
}

func (a *taxonomyAPI) server(t *testing.T, status int, resp any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.method, a.path, a.query, a.auth = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		a.body = nil
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.body)
		}
		if resp == nil {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func categoryResp(slug, name string, archived, isDefault bool) map[string]any {
	return map[string]any{"category": map[string]any{
		"id": "c-1", "slug": slug, "name": name, "archived": archived, "is_default": isDefault,
	}}
}

func runTaxonomy(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	resetControlsAuth(t)
	return runImportCmd(t, append([]string{"--endpoint", srv.URL, "--token", "tok", "taxonomy", "categories"}, args...)...)
}

func TestTaxonomyCategoriesList_PrintsUsageAndStatus(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, map[string]any{"categories": []map[string]any{
		{"slug": "availability", "name": "Availability", "risk_count": 1, "rule_count": 1},
		{"slug": "operational", "name": "Operational Resilience", "risk_count": 4, "is_default": true},
		{"slug": "privacy", "name": "Privacy", "archived": true},
	}})

	out, err := runTaxonomy(t, srv, "list", "--include-archived")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if api.method != http.MethodGet || api.path != "/v1/risks/categories" || api.query != "include_archived=true" || api.auth != "Bearer tok" {
		t.Errorf("request %s %s?%s auth=%q", api.method, api.path, api.query, api.auth)
	}
	for _, want := range []string{
		"SLUG", "RISKS", "RULES",
		"availability  Availability            1      1      active",
		"operational   Operational Resilience  4      0      default",
		"privacy       Privacy                 0      0      archived",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	if _, err := runTaxonomy(t, srv, "list"); err != nil || api.query != "" {
		t.Errorf("list without --include-archived: err=%v query=%q", err, api.query)
	}
}

func TestTaxonomyCategoriesAdd_SendsSlugNameDescription(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusCreated, categoryResp("cloud-security", "Cloud Security", false, false))

	out, err := runTaxonomy(t, srv, "add", "cloud-security", "--name", "Cloud Security", "--description", "IaaS")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if api.method != http.MethodPost || api.path != "/v1/risks/categories" ||
		api.body["slug"] != "cloud-security" || api.body["name"] != "Cloud Security" || api.body["description"] != "IaaS" {
		t.Errorf("request %s %s body=%v", api.method, api.path, api.body)
	}
	if !strings.Contains(out, "added cloud-security (Cloud Security) [active]") {
		t.Errorf("output = %s", out)
	}
	if _, err := runTaxonomy(t, srv, "add", "x"); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Errorf("add without --name: err = %v", err)
	}
}

func TestTaxonomyCategoriesRename_SendsOnlyChangedFields(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, categoryResp("operational", "Operational Resilience", false, true))

	out, err := runTaxonomy(t, srv, "rename", "operational", "--name", "Operational Resilience")
	if err != nil {
		t.Fatalf("rename: %v\n%s", err, out)
	}
	if api.method != http.MethodPatch || api.path != "/v1/risks/categories/operational" {
		t.Errorf("request %s %s", api.method, api.path)
	}
	if _, has := api.body["description"]; has || api.body["name"] != "Operational Resilience" {
		t.Errorf("body = %v, want name only", api.body)
	}
	if !strings.Contains(out, "renamed operational (Operational Resilience) [default]") {
		t.Errorf("output = %s", out)
	}
	if _, err := runTaxonomy(t, srv, "rename", "operational"); err == nil {
		t.Error("rename with no flags succeeded")
	}
}

func TestTaxonomyCategoriesArchiveUnarchiveSetDefault(t *testing.T) {
	for _, tc := range []struct {
		action, want  string
		archived, def bool
	}{
		{"archive", "archived privacy (Privacy) [archived]", true, false},
		{"unarchive", "unarchived privacy (Privacy) [active]", false, false},
		{"set-default", "set as default privacy (Privacy) [default]", false, true},
	} {
		t.Run(tc.action, func(t *testing.T) {
			api := &taxonomyAPI{}
			srv := api.server(t, http.StatusOK, categoryResp("privacy", "Privacy", tc.archived, tc.def))
			out, err := runTaxonomy(t, srv, tc.action, "privacy")
			if err != nil {
				t.Fatalf("%s: %v\n%s", tc.action, err, out)
			}
			if api.method != http.MethodPost || api.path != "/v1/risks/categories/privacy/"+tc.action {
				t.Errorf("request %s %s", api.method, api.path)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output = %s, want %q", out, tc.want)
			}
		})
	}
}

func TestTaxonomyCategoriesDelete(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusNoContent, nil)
	out, err := runTaxonomy(t, srv, "delete", "financial")
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	if api.method != http.MethodDelete || api.path != "/v1/risks/categories/financial" {
		t.Errorf("request %s %s", api.method, api.path)
	}
	if !strings.Contains(out, "deleted financial") {
		t.Errorf("output = %s", out)
	}
}

func TestTaxonomyCategoriesDelete_InUseSurfacesCounts(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusConflict, map[string]any{
		"error":      `risk: category "privacy" is used by 3 risk(s) and 1 aggregation rule(s); archive it instead`,
		"risk_count": 3, "rule_count": 1,
	})
	_, err := runTaxonomy(t, srv, "delete", "privacy")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "3 risk(s) and 1 aggregation rule(s)") {
		t.Fatalf("err = %v, want the 409 with counts", err)
	}
}
