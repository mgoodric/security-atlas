package confluenceapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestResolveCredential(t *testing.T) {
	t.Setenv(EnvEmail, "")
	t.Setenv(EnvToken, "")
	if _, err := ResolveCredential("", "tok"); err == nil || !strings.Contains(err.Error(), EnvEmail) {
		t.Errorf("no email: err = %v", err)
	}
	if _, err := ResolveCredential("a@example.com", ""); err == nil || !strings.Contains(err.Error(), EnvToken) {
		t.Errorf("no token: err = %v", err)
	}
	t.Setenv(EnvEmail, "env@example.com")
	t.Setenv(EnvToken, "env-secret")
	c, err := ResolveCredential("", "")
	if err != nil {
		t.Fatal(err)
	}
	if s := c.String(); strings.Contains(s, "env@example.com") || strings.Contains(s, "env-secret") || strings.Contains(s, c.secret) {
		t.Errorf("String leaks the credential: %s", s)
	}
	if s := fmt.Sprintf("%v", c); strings.Contains(s, "env-secret") {
		t.Errorf("%%v leaks the credential: %s", s)
	}
}

func TestCheckBaseURL(t *testing.T) {
	t.Parallel()
	for raw, ok := range map[string]bool{
		"https://acme.atlassian.net":   true,
		"https://acme.atlassian.net/":  true,
		"http://127.0.0.1:8080":        true,
		"http://localhost:8080":        true,
		"http://[::1]:8080":            true,
		"http://acme.atlassian.net":    false,
		"ftp://acme.atlassian.net":     false,
		"acme.atlassian.net":           false,
		"https://u:p@acme.example":     false,
		"https://acme.example/#x":      false,
		"https://acme.example/?a=b":    false,
		"https://acme.example/%zz":     false,
		"https://atlas.example.com/sa": true,
	} {
		_, err := CheckBaseURL("--x", raw)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestCQL(t *testing.T) {
	t.Parallel()
	got, err := CQL(" POL ", "policy")
	if err != nil || got != `type = page AND space = "POL" AND label = "policy" ORDER BY id` {
		t.Errorf("CQL = %q, %v", got, err)
	}
	for _, bad := range [][2]string{{"", "policy"}, {"POL", " "}, {`P"OL`, "policy"}, {"POL", `a\b`}, {"POL", "a\nb"}} {
		if _, err := CQL(bad[0], bad[1]); err == nil {
			t.Errorf("CQL(%q, %q): want an error", bad[0], bad[1])
		}
	}
}

// server answers each search with the handler for its request index.
func server(t *testing.T, handlers ...func(w http.ResponseWriter, r *http.Request)) (*Client, *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n
		n++
		if i >= len(handlers) {
			http.Error(w, "too many requests", http.StatusTeapot)
			return
		}
		handlers[i](w, r)
	}))
	t.Cleanup(srv.Close)
	site, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cred, _ := ResolveCredential("e@example.com", "t")
	return &Client{HTTP: srv.Client(), Site: site, Cred: cred}, &n
}

func answer(body map[string]any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(body) }
}

func result(id string) map[string]any {
	return map[string]any{"id": id, "type": "page", "title": "T" + id, "body": map[string]any{"storage": map[string]any{"value": "<p>" + id + "</p>"}}}
}

func TestLabelledPages_Pagination(t *testing.T) {
	t.Parallel()
	var nextPaths []string
	c, _ := server(t,
		answer(map[string]any{"results": []any{result("1"), result("2")}, "_links": map[string]any{"context": "/wiki", "next": "/rest/api/content/search?cursor=a"}}),
		func(w http.ResponseWriter, r *http.Request) {
			nextPaths = append(nextPaths, r.URL.RequestURI())
			// Already carries the context; a repeated id and a non-page
			// result are dropped.
			answer(map[string]any{"results": []any{result("2"), result("3"), map[string]any{"id": "9", "type": "blogpost"}}, "_links": map[string]any{"next": "/wiki/rest/api/content/search?cursor=b"}})(w, r)
		},
		func(w http.ResponseWriter, r *http.Request) {
			nextPaths = append(nextPaths, r.URL.RequestURI())
			answer(map[string]any{"results": []any{result("4")}, "_links": map[string]any{}})(w, r)
		},
	)
	pages, err := c.LabelledPages(context.Background(), "POL", "policy")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range pages {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "1,2,3,4" {
		t.Errorf("ids = %v", ids)
	}
	want := []string{"/wiki/rest/api/content/search?cursor=a", "/wiki/rest/api/content/search?cursor=b"}
	if strings.Join(nextPaths, " ") != strings.Join(want, " ") {
		t.Errorf("next requests = %v, want %v", nextPaths, want)
	}
	// No base/webui in the response: the viewpage URL on the site.
	if want := c.Site.String() + "/wiki/pages/viewpage.action?pageId=1"; pages[0].URL != want {
		t.Errorf("URL = %q, want %q", pages[0].URL, want)
	}
	if pages[0].Storage != "<p>1</p>" || pages[0].Title != "T1" {
		t.Errorf("page = %+v", pages[0])
	}
}

func TestLabelledPages_PageURL(t *testing.T) {
	t.Parallel()
	c, _ := server(t)
	base := c.Site.String() + "/wiki"
	for _, tc := range []struct{ base, webui, want string }{
		{base, "/spaces/POL/pages/7/Title", base + "/spaces/POL/pages/7/Title"},
		{"https://evil.example/wiki", "/spaces/POL/pages/7", c.Site.String() + "/wiki/pages/viewpage.action?pageId=7"},
		{base, "javascript:alert(1)", c.Site.String() + "/wiki/pages/viewpage.action?pageId=7"},
		{"", "/spaces/POL/pages/7", c.Site.String() + "/wiki/pages/viewpage.action?pageId=7"},
	} {
		if got := c.pageURL(tc.base, tc.webui, "7"); got != tc.want {
			t.Errorf("pageURL(%q, %q) = %q, want %q", tc.base, tc.webui, got, tc.want)
		}
	}
}

func TestLabelledPages_Failures(t *testing.T) {
	t.Parallel()
	page1 := map[string]any{"results": []any{result("1")}, "_links": map[string]any{"next": "/rest/api/content/search?cursor=a"}}
	cases := map[string]struct {
		handlers []func(http.ResponseWriter, *http.Request)
		maxPages int
		want     string
	}{
		"error on a later page": {
			handlers: []func(http.ResponseWriter, *http.Request){answer(page1), func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
			}},
			want: "HTTP 429",
		},
		"unreadable response": {
			handlers: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }},
			want:     "decode",
		},
		"next link to another host": {
			handlers: []func(http.ResponseWriter, *http.Request){answer(map[string]any{"results": []any{result("1")}, "_links": map[string]any{"next": "https://evil.example/wiki/rest/api/content/search"}})},
			want:     "another host",
		},
		"unparseable next link": {
			handlers: []func(http.ResponseWriter, *http.Request){answer(map[string]any{"results": []any{result("1")}, "_links": map[string]any{"next": "%zz"}})},
			want:     "unreadable next link",
		},
		"next link with nothing new": {
			handlers: []func(http.ResponseWriter, *http.Request){answer(page1), answer(page1)},
			want:     "no new pages",
		},
		"more than the cap": {
			handlers: []func(http.ResponseWriter, *http.Request){answer(map[string]any{"results": []any{result("1"), result("2"), result("3")}, "_links": map[string]any{}})},
			maxPages: 2,
			want:     "too many labelled pages",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, _ := server(t, tc.handlers...)
			c.MaxPages = tc.maxPages
			pages, err := c.LabelledPages(context.Background(), "POL", "policy")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if pages != nil {
				t.Errorf("pages = %v, want none with an error", pages)
			}
		})
	}
}

func TestLabelledPages_LoopBound(t *testing.T) {
	t.Parallel()
	// Each response has one new page and a next link, past MaxPages
	// requests: the cap on pages ends it.
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i++
		answer(map[string]any{"results": []any{result(fmt.Sprint(i))}, "_links": map[string]any{"next": "/rest/api/content/search?cursor=x"}})(w, nil)
	}))
	defer srv.Close()
	site, _ := url.Parse(srv.URL)
	c := &Client{HTTP: srv.Client(), Site: site, MaxPages: 3}
	_, err := c.LabelledPages(context.Background(), "POL", "policy")
	if !errors.Is(err, ErrTooManyPages) {
		t.Fatalf("err = %v, want ErrTooManyPages", err)
	}
}

func TestLabelledPages_BadInput(t *testing.T) {
	t.Parallel()
	if _, err := (&Client{}).LabelledPages(context.Background(), "POL", "policy"); err == nil {
		t.Error("empty client: want an error")
	}
	c, _ := server(t)
	if _, err := c.LabelledPages(context.Background(), "", "policy"); err == nil {
		t.Error("empty space: want an error")
	}
	c.Site = &url.URL{Scheme: "http", Host: "127.0.0.1:1"}
	if _, err := c.LabelledPages(context.Background(), "POL", "policy"); err == nil {
		t.Error("unreachable site: want an error")
	}
}
