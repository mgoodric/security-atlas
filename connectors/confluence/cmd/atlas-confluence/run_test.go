package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests replay Confluence Cloud's CQL content search
// (GET /wiki/rest/api/content/search, body.storage expanded, cursor
// pagination through _links.next) and stand an httptest server in for
// the platform's POST /v1/policies:sync. The Jira connector's
// integration_test.go is the prior art.

const (
	testEmail = "policy-bot@example.com"
	testToken = "confluence-secret-token"
	testKey   = "atlas_test_policy_sync_key"
)

func props(rows ...string) string {
	var b strings.Builder
	b.WriteString(`<ac:structured-macro ac:name="details" ac:schema-version="1"><ac:rich-text-body><table><tbody>`)
	for i := 0; i+1 < len(rows); i += 2 {
		fmt.Fprintf(&b, `<tr><th><p>%s</p></th><td><p>%s</p></td></tr>`, rows[i], rows[i+1])
	}
	b.WriteString(`</tbody></table></ac:rich-text-body></ac:structured-macro>`)
	return b.String()
}

var fullProps = props(
	"Version", "1.0",
	"Approved by", "Ada Lovelace",
	"Approval date", `<time datetime="2026-03-01" />`,
	"Effective date", "2026-03-02",
	"Next review date", "2027-03-01",
	"Acknowledgment audience", "everyone, control_owner",
)

type fakePage struct {
	ID, Title, Storage string
}

// fakeConfluence serves pages two per response. failOn makes the request
// with that index (0-based) answer 500.
type fakeConfluence struct {
	t      *testing.T
	pages  []fakePage
	failOn int
	srv    *httptest.Server

	mu       sync.Mutex
	requests int
}

func newFakeConfluence(t *testing.T, pages []fakePage, failOn int) *fakeConfluence {
	t.Helper()
	fc := &fakeConfluence{t: t, pages: pages, failOn: failOn}
	fc.srv = httptest.NewServer(http.HandlerFunc(fc.serve))
	t.Cleanup(fc.srv.Close)
	return fc
}

func (fc *fakeConfluence) serve(w http.ResponseWriter, r *http.Request) {
	fc.mu.Lock()
	n := fc.requests
	fc.requests++
	fc.mu.Unlock()

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testToken))
	if r.Method != http.MethodGet || r.URL.Path != "/wiki/rest/api/content/search" || r.Header.Get("Authorization") != wantAuth {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	if n == fc.failOn {
		http.Error(w, `{"statusCode":500,"message":"internal error"}`, http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	start := 0
	if c := q.Get("cursor"); c != "" {
		_, _ = fmt.Sscanf(c, "c%d", &start)
	} else {
		if got, want := q.Get("cql"), `type = page AND space = "POL" AND label = "policy" ORDER BY id`; got != want {
			http.Error(w, "cql = "+got, http.StatusBadRequest)
			return
		}
		if q.Get("expand") != "body.storage" {
			http.Error(w, "expand = "+q.Get("expand"), http.StatusBadRequest)
			return
		}
	}
	end := min(start+2, len(fc.pages))
	results := []map[string]any{}
	for _, p := range fc.pages[start:end] {
		results = append(results, map[string]any{
			"id": p.ID, "type": "page", "status": "current", "title": p.Title,
			"body":   map[string]any{"storage": map[string]any{"value": p.Storage, "representation": "storage"}},
			"_links": map[string]any{"webui": "/spaces/POL/pages/" + p.ID, "self": fc.srv.URL + "/wiki/rest/api/content/" + p.ID},
		})
	}
	links := map[string]any{"base": fc.srv.URL + "/wiki", "context": "/wiki"}
	if end < len(fc.pages) {
		links["next"] = fmt.Sprintf("/rest/api/content/search?cql=x&limit=2&cursor=c%d", end)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "start": start, "limit": 2, "size": len(results), "_links": links})
}

func (fc *fakeConfluence) requestCount() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.requests
}

// fakePlatform records each push and answers with a report: a page whose
// properties are null is skipped, every other page created. status, when
// set, is answered instead.
type fakePlatform struct {
	status int
	srv    *httptest.Server

	mu     sync.Mutex
	pushes []map[string]any
	auths  []string
}

func newFakePlatform(t *testing.T, status int) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{status: status}
	fp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if r.Method != http.MethodPost || r.URL.Path != "/v1/policies:sync" || json.Unmarshal(body, &req) != nil {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		fp.mu.Lock()
		fp.pushes = append(fp.pushes, req)
		fp.auths = append(fp.auths, r.Header.Get("Authorization"))
		fp.mu.Unlock()
		if fp.status != 0 {
			http.Error(w, `{"error":"policy sync requires a policy-sync API key"}`, fp.status)
			return
		}
		rep := map[string]any{"results": []any{}}
		created, skipped := 0, 0
		var results []any
		for _, p := range req["pages"].([]any) {
			page := p.(map[string]any)
			if page["properties"] == nil {
				skipped++
				results = append(results, map[string]any{"page_id": page["id"], "title": page["title"], "status": "skipped", "reason": "missing page properties"})
				continue
			}
			created++
			results = append(results, map[string]any{"page_id": page["id"], "title": page["title"], "status": "created", "policy_id": "pol-" + page["id"].(string), "version": "1.0"})
		}
		rep["created"], rep["skipped"], rep["results"] = created, skipped, results
		_ = json.NewEncoder(w).Encode(rep)
	}))
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakePlatform) recorded() ([]map[string]any, []string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.pushes, fp.auths
}

func runAgainst(t *testing.T, fc *fakeConfluence, fp *fakePlatform) (string, error) {
	t.Helper()
	t.Setenv("CONFLUENCE_EMAIL", testEmail)
	t.Setenv("CONFLUENCE_API_TOKEN", testToken)
	t.Setenv(EnvAtlasKey, testKey)
	t.Setenv(EnvSite, fc.srv.URL+"/")
	t.Setenv(EnvSpace, "POL")
	t.Setenv(EnvLabel, "")
	t.Setenv(EnvAtlasURL, fp.srv.URL)
	var out bytes.Buffer
	cmd := newRootCmd(&out)
	cmd.SetArgs([]string{"run", "--timeout", "30s"})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return out.String(), err
}

func TestRun_FullSetAcrossPages(t *testing.T) {
	pages := []fakePage{
		{ID: "101", Title: "Access Control Policy", Storage: fullProps + `<h1>Purpose</h1><p>Least privilege.</p>`},
		{ID: "102", Title: "Incident Response Policy", Storage: fullProps + `<p>Respond.</p><ac:structured-macro ac:name="toc" />`},
		{ID: "103", Title: "Unlabelled Template", Storage: `<p>No properties table here.</p>`},
		{ID: "104", Title: "Vendor Policy", Storage: props("Version", "2.1", "Approved by", "Grace Hopper", "Approval date", "2026-01-05", "Effective date", "2026-01-06", "Reviewed by", "someone") + `<p>Vendors.</p>`},
		{ID: "105", Title: "Data Policy", Storage: fullProps + `<p>Data.</p>`},
	}
	fc := newFakeConfluence(t, pages, -1)
	fp := newFakePlatform(t, 0)

	out, err := runAgainst(t, fc, fp)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if got := fc.requestCount(); got != 3 {
		t.Errorf("confluence requests = %d, want 3 (5 pages, 2 per response)", got)
	}
	pushes, auths := fp.recorded()
	if len(pushes) != 1 {
		t.Fatalf("pushes = %d, want exactly 1", len(pushes))
	}
	if auths[0] != "Bearer "+testKey {
		t.Errorf("platform Authorization = %q, want the policy-sync key", auths[0])
	}
	push := pushes[0]
	src := push["source"].(map[string]any)
	if src["site"] != fc.srv.URL || src["space"] != "POL" {
		t.Errorf("source = %v", src)
	}
	got := push["pages"].([]any)
	if len(got) != 5 {
		t.Fatalf("pushed %d pages, want all 5", len(got))
	}

	first := got[0].(map[string]any)
	if first["id"] != "101" || first["title"] != "Access Control Policy" {
		t.Errorf("first page = %v", first)
	}
	if want := fc.srv.URL + "/wiki/spaces/POL/pages/101"; first["url"] != want {
		t.Errorf("url = %v, want %s", first["url"], want)
	}
	if first["body_md"] != "# Purpose\n\nLeast privilege.\n" {
		t.Errorf("body_md = %q (the properties table must not be in the body)", first["body_md"])
	}
	wantProps := map[string]any{
		"version": "1.0", "approved_by_name": "Ada Lovelace", "approved_at": "2026-03-01",
		"effective_date": "2026-03-02", "next_review_at": "2027-03-01",
		"acknowledgment_required_roles": []any{"everyone", "control_owner"},
	}
	if g, w := mustJSON(t, first["properties"]), mustJSON(t, wantProps); g != w {
		t.Errorf("properties = %s\nwant %s", g, w)
	}

	// The page missing properties is pushed with null properties, so the
	// platform reports it.
	missing := got[2].(map[string]any)
	if v, ok := missing["properties"]; !ok || v != nil {
		t.Errorf("page 103 properties = %v (present %v), want null", v, ok)
	}
	// An unrecognised row is passed through for the platform to refuse.
	vendor := got[3].(map[string]any)["properties"].(map[string]any)
	if vendor["Reviewed by"] != "someone" {
		t.Errorf("page 104 properties = %v, want the unknown row passed through", vendor)
	}

	for _, want := range []string{
		`page 101 "Access Control Policy": created version=1.0 policy=pol-101`,
		`page 103 "Unlabelled Template": skipped reason=missing page properties`,
		`page 102 "Incident Response Policy": unsupported macros: toc x1`,
		`sync: created=4 new_version=0 taken_over=0 unchanged=0 drift=0 skipped=1 missing=0 skipped_by_connector=0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}
	for _, secret := range []string{testToken, testKey} {
		if strings.Contains(out, secret) {
			t.Errorf("report leaks a secret")
		}
	}
}

func TestRun_ConfluenceErrorMidReadPushesNothing(t *testing.T) {
	pages := []fakePage{
		{ID: "1", Title: "A", Storage: fullProps + "<p>a</p>"},
		{ID: "2", Title: "B", Storage: fullProps + "<p>b</p>"},
		{ID: "3", Title: "C", Storage: fullProps + "<p>c</p>"},
	}
	fc := newFakeConfluence(t, pages, 1) // the second response fails
	fp := newFakePlatform(t, 0)

	_, err := runAgainst(t, fc, fp)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "nothing pushed") {
		t.Fatalf("err = %v, want the HTTP 500 and nothing pushed", err)
	}
	if pushes, _ := fp.recorded(); len(pushes) != 0 {
		t.Fatalf("pushes = %d, want 0 after a failed read", len(pushes))
	}
}

func TestRun_PlatformRefusalFails(t *testing.T) {
	fc := newFakeConfluence(t, []fakePage{{ID: "1", Title: "A", Storage: fullProps + "<p>a</p>"}}, -1)
	fp := newFakePlatform(t, http.StatusForbidden)
	_, err := runAgainst(t, fc, fp)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("err = %v, want HTTP 403", err)
	}
}

func TestRun_EmptySetPushesNothing(t *testing.T) {
	fc := newFakeConfluence(t, nil, -1)
	fp := newFakePlatform(t, 0)
	_, err := runAgainst(t, fc, fp)
	if err == nil || !strings.Contains(err.Error(), "no pages labelled") {
		t.Fatalf("err = %v, want the empty-set refusal", err)
	}
	if pushes, _ := fp.recorded(); len(pushes) != 0 {
		t.Fatalf("pushes = %d, want 0", len(pushes))
	}
}

func TestRun_UnconvertibleBodyIsReportedAndLeftOut(t *testing.T) {
	deep := strings.Repeat("<div>", 600)
	fc := newFakeConfluence(t, []fakePage{
		{ID: "1", Title: "Good", Storage: fullProps + "<p>a</p>"},
		{ID: "2", Title: "Deep", Storage: deep},
	}, -1)
	fp := newFakePlatform(t, 0)
	out, err := runAgainst(t, fc, fp)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	pushes, _ := fp.recorded()
	if n := len(pushes[0]["pages"].([]any)); n != 1 {
		t.Errorf("pushed %d pages, want 1", n)
	}
	if !strings.Contains(out, `page 2 "Deep": skipped by connector reason=body not converted`) ||
		!strings.Contains(out, "skipped_by_connector=1") {
		t.Errorf("report:\n%s", out)
	}

	// When no body converts at all, nothing is pushed.
	fc2 := newFakeConfluence(t, []fakePage{{ID: "2", Title: "Deep", Storage: deep}}, -1)
	fp2 := newFakePlatform(t, 0)
	if _, err := runAgainst(t, fc2, fp2); err == nil {
		t.Fatal("want an error when no page converts")
	}
	if pushes, _ := fp2.recorded(); len(pushes) != 0 {
		t.Fatalf("pushes = %d, want 0", len(pushes))
	}
}

func TestRun_Configuration(t *testing.T) {
	for _, k := range []string{EnvSite, EnvSpace, EnvLabel, EnvAtlasURL, EnvAtlasKey, "CONFLUENCE_EMAIL", "CONFLUENCE_API_TOKEN"} {
		t.Setenv(k, "")
	}
	cmd := newRootCmd(io.Discard)
	cmd.SetArgs([]string{"run"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want an error with no configuration")
	}
	for _, want := range []string{"--site", "--space", "--atlas-url", EnvAtlasKey} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}

	c := config{site: "https://acme.atlassian.net", space: "POL", atlasURL: "https://atlas.example.com", atlasKey: "k"}
	if err := c.resolve(); err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Errorf("zero timeout: err = %v", err)
	}
	if c.label != defaultLabel {
		t.Errorf("label = %q, want the default %q", c.label, defaultLabel)
	}

	ctx := context.Background()
	t.Setenv("CONFLUENCE_API_TOKEN", "t")
	for name, c := range map[string]config{
		"http site":       {site: "http://acme.atlassian.net", space: "POL", atlasURL: "https://a.example", atlasKey: "k", timeout: time.Minute},
		"http platform":   {site: "https://acme.atlassian.net", space: "POL", atlasURL: "http://a.example", atlasKey: "k", timeout: time.Minute},
		"no email":        {site: "https://acme.atlassian.net", space: "POL", atlasURL: "https://a.example", atlasKey: "k", timeout: time.Minute},
		"quote in space":  {site: "https://acme.atlassian.net", space: `P"OL`, atlasURL: "https://a.example", atlasKey: "k", timeout: time.Minute, confluenceEmail: "e"},
		"relative site":   {site: "acme", space: "POL", atlasURL: "https://a.example", atlasKey: "k", timeout: time.Minute},
		"site with query": {site: "https://acme.atlassian.net?x=1", space: "POL", atlasURL: "https://a.example", atlasKey: "k", timeout: time.Minute},
	} {
		if err := run(ctx, c, http.DefaultClient, io.Discard); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The secrets have no flags, so they can never reach ps or shell history.
func TestRun_NoSecretFlags(t *testing.T) {
	for _, flag := range []string{"--confluence-token", "--atlas-key"} {
		cmd := newRootCmd(io.Discard)
		cmd.SetArgs([]string{"run", flag, "secret"})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: err = %v, want unknown flag", flag, err)
		}
	}
}

// A redirect off the configured origin is refused for both the
// Confluence read and the platform push, and the other host never sees
// a request.
func TestRun_RefusesRedirectToAnotherHost(t *testing.T) {
	var mu sync.Mutex
	elsewhere := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		elsewhere++
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer other.Close()
	redirect := func(code int) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, code)
		}))
		t.Cleanup(s.Close)
		return s
	}

	// Confluence redirects.
	fp := newFakePlatform(t, 0)
	fc := &fakeConfluence{srv: redirect(http.StatusFound)}
	_, err := runAgainst(t, fc, fp)
	if err == nil || !strings.Contains(err.Error(), "refused redirect") {
		t.Fatalf("confluence redirect: err = %v, want refused", err)
	}
	if pushes, _ := fp.recorded(); len(pushes) != 0 {
		t.Errorf("pushed after a refused read")
	}

	// The platform redirects, keeping the method and body (307).
	fc = newFakeConfluence(t, []fakePage{{ID: "1", Title: "A", Storage: fullProps + "<p>a</p>"}}, -1)
	fp = &fakePlatform{srv: redirect(http.StatusTemporaryRedirect)}
	_, err = runAgainst(t, fc, fp)
	if err == nil || !strings.Contains(err.Error(), "refused redirect") {
		t.Fatalf("platform redirect: err = %v, want refused", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if elsewhere != 0 {
		t.Errorf("the other host got %d requests, want 0", elsewhere)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
