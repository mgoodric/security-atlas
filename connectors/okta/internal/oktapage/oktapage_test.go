package oktapage_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapage"
)

type item struct {
	ID string `json:"id"`
}

func creds(t *testing.T) oktaauth.Credential {
	t.Helper()
	c, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNextLink(t *testing.T) {
	// Okta's documented headers: self and next as separate Link values.
	okta := []string{
		`<https://example.okta.com/api/v1/users?limit=200>; rel="self"`,
		`<https://example.okta.com/api/v1/users?after=00ubfjQEMYBLRUWIEDKK&limit=200>; rel="next"`,
	}
	cases := []struct {
		name    string
		headers []string
		want    string
	}{
		{"okta self+next", okta, "https://example.okta.com/api/v1/users?after=00ubfjQEMYBLRUWIEDKK&limit=200"},
		{"self only (last page)", okta[:1], ""},
		{"comma-joined", []string{strings.Join(okta, ", ")}, "https://example.okta.com/api/v1/users?after=00ubfjQEMYBLRUWIEDKK&limit=200"},
		{"none", nil, ""},
		{"unbracketed target ignored", []string{`https://x/; rel="next"`}, ""},
	}
	for _, tc := range cases {
		if got := oktapage.NextLink(tc.headers); got != tc.want {
			t.Errorf("%s: NextLink = %q; want %q", tc.name, got, tc.want)
		}
	}
}

// pagedServer serves n pages of one item each at /api/v1/things, linking
// each to the next with an `after` cursor, as Okta does.
func pagedServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "SSWS test-token" {
			t.Errorf("Authorization = %q", got)
		}
		page := 1
		if a := r.URL.Query().Get("after"); a != "" {
			_, _ = fmt.Sscanf(a, "p%d", &page)
		}
		w.Header().Add("Link", "<"+srv.URL+r.URL.RequestURI()+`>; rel="self"`)
		if page < n {
			w.Header().Add("Link", fmt.Sprintf(`<%s/api/v1/things?after=p%d&limit=1>; rel="next"`, srv.URL, page+1))
		}
		_, _ = fmt.Fprintf(w, `[{"id":"i%d"}]`, page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestList_FollowsNextAcrossPages(t *testing.T) {
	srv := pagedServer(t, 3)
	got, err := oktapage.List[item](context.Background(), srv.Client(), srv.URL, creds(t), "/api/v1/things?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "i1" || got[2].ID != "i3" {
		t.Fatalf("items = %+v; want i1..i3 in order", got)
	}
}

func TestList_RefusesNextOnAnotherHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Link", `<https://evil.example.com/api/v1/things?after=x>; rel="next"`)
		_, _ = w.Write([]byte(`[{"id":"a"}]`))
	}))
	t.Cleanup(srv.Close)
	_, err := oktapage.List[item](context.Background(), srv.Client(), srv.URL, creds(t), "/api/v1/things")
	if err == nil || !strings.Contains(err.Error(), "leaves") {
		t.Fatalf("err = %v; want a refusal to leave the base host", err)
	}
}

func TestList_CapsPages(t *testing.T) {
	srv := pagedServer(t, oktapage.MaxPages+1)
	_, err := oktapage.List[item](context.Background(), srv.Client(), srv.URL, creds(t), "/api/v1/things?limit=1")
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v; want the page cap", err)
	}
}

func TestList_APIErrorOnLaterPage(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "" {
			http.Error(w, `{"errorCode":"E0000047"}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Add("Link", "<"+srv.URL+`/api/v1/things?after=c>; rel="next"`)
		_, _ = w.Write([]byte(`[{"id":"a"}]`))
	}))
	t.Cleanup(srv.Close)
	_, err := oktapage.List[item](context.Background(), srv.Client(), srv.URL, creds(t), "/api/v1/things")
	var ae *oktapage.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests || !strings.Contains(ae.Body, "E0000047") {
		t.Fatalf("err = %v; want APIError 429 with body", err)
	}
}
