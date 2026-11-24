package graph_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/shared/policysync"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/graph"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/graph/graphtest"
)

func site(t *testing.T) *url.URL {
	t.Helper()
	u, err := graph.CheckSiteURL("https://" + graphtest.SiteHost + graphtest.SitePath)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func client(t *testing.T, g *graphtest.Server) *graph.Client {
	t.Helper()
	t.Setenv(graph.EnvClientSecret, graphtest.ClientSecret)
	cred, err := graph.ResolveCredential(graphtest.Tenant, graphtest.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	hc := g.Client()
	hc.CheckRedirect = policysync.CheckRedirect
	return &graph.Client{HTTP: hc, Cred: cred}
}

func TestCheckSiteURL(t *testing.T) {
	t.Parallel()
	for raw, ok := range map[string]bool{
		"https://contoso.sharepoint.com/sites/Policies":       true,
		"https://contoso.sharepoint.com/sites/Policy%20Hub/":  true,
		"https://contoso.sharepoint.com/teams/Security":       true,
		"https://contoso.sharepoint.com":                      false,
		"https://sharepoint.com/sites/x":                      false,
		"https://contoso.sharepoint.com.evil.example/sites/x": false,
		"http://contoso.sharepoint.com/sites/x":               false,
		"https://contoso.sharepoint.com:443/sites/x":          false,
		"https://u@contoso.sharepoint.com/sites/x":            false,
		"https://contoso.sharepoint.com/sites/x?a=b":          false,
		"https://contoso.sharepoint.com/sites/x#f":            false,
		"contoso.sharepoint.com/sites/x":                      false,
		"https://contoso.sharepoint.com/%zz":                  false,
	} {
		if _, err := graph.CheckSiteURL(raw); (err == nil) != ok {
			t.Errorf("CheckSiteURL(%q) err = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestResolveCredential(t *testing.T) {
	t.Setenv(graph.EnvTenantID, "")
	t.Setenv(graph.EnvClientID, "")
	t.Setenv(graph.EnvClientSecret, "")
	for _, tc := range []struct{ tenant, client, secret, want string }{
		{"", "c", "s", "tenant id required"},
		{"a/b", "c", "s", "tenant id must be"},
		{"t", "", "s", "client id required"},
		{"t", "c", "", "client secret required"},
	} {
		t.Setenv(graph.EnvClientSecret, tc.secret)
		if _, err := graph.ResolveCredential(tc.tenant, tc.client); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ResolveCredential(%q, %q) err = %v, want %q", tc.tenant, tc.client, err, tc.want)
		}
	}
	t.Setenv(graph.EnvTenantID, "contoso.onmicrosoft.com")
	t.Setenv(graph.EnvClientID, "cid")
	t.Setenv(graph.EnvClientSecret, "very-secret")
	cred, err := graph.ResolveCredential("", "")
	if err != nil {
		t.Fatal(err)
	}
	if s := cred.String(); strings.Contains(s, "very-secret") || !strings.Contains(s, "<redacted>") {
		t.Errorf("String() = %q", s)
	}
}

func TestDocuments(t *testing.T) {
	g := graphtest.New(t,
		graphtest.Item{ID: "1", Name: "A.docx", Content: []byte("a")},
		graphtest.Item{ID: "2", Name: "Folder", Folder: true},
		graphtest.Item{ID: "3", Name: "B.PDF"},
	)
	g.ItemsPerPage = 1
	c := client(t, g)
	lib, docs, err := c.Documents(context.Background(), site(t), " POLICIES ")
	if err != nil {
		t.Fatal(err)
	}
	if lib.ListID != graphtest.ListID || lib.Name != graphtest.Library {
		t.Errorf("library = %+v", lib)
	}
	if len(docs) != 2 || docs[0].ID != "1" || string(docs[0].Content) != "a" || !docs[0].IsDocx() || docs[1].IsDocx() || docs[1].Content != nil {
		t.Fatalf("docs = %+v", docs)
	}

	c.MaxDocuments = 1
	if _, _, err := c.Documents(context.Background(), site(t), "Policies"); !errors.Is(err, graph.ErrTooManyDocuments) {
		t.Errorf("over MaxDocuments: err = %v", err)
	}
}

func TestDocumentsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		library string
		setup   func(*graphtest.Server)
		want    string
	}{
		{"no library", "Missing", nil, `no document library named "Missing"`},
		{"blank library", " ", nil, "library is required"},
		{"looping next link", "Policies", func(g *graphtest.Server) {
			g.NextLink = "https://graph.microsoft.com/v1.0/sites/" + url.PathEscape(graphtest.SiteID) + "/lists/" + graphtest.ListID + "/items?expand=fields"
		}, "next link with no new items"},
		{"unreadable next link", "Policies", func(g *graphtest.Server) { g.NextLink = "https://graph.microsoft.com/%zz" }, "unreadable URL"},
		{"bad token", "Policies", func(g *graphtest.Server) { g.Fail = map[string]int{"/oauth2/": http.StatusBadRequest} }, "graph: token: HTTP 400"},
	} {
		if tc.setup == nil {
			tc.setup = func(*graphtest.Server) {}
		}
		t.Run(tc.name, func(t *testing.T) {
			g := graphtest.New(t, graphtest.Item{ID: "1", Name: "A.docx"}, graphtest.Item{ID: "2", Name: "B.docx"})
			g.ItemsPerPage = 1
			tc.setup(g)
			_, docs, err := client(t, g).Documents(context.Background(), site(t), tc.library)
			if err == nil || !strings.Contains(err.Error(), tc.want) || docs != nil {
				t.Fatalf("err = %v, docs = %v; want %q and no documents", err, docs, tc.want)
			}
		})
	}
}

func TestDocumentsNeedsClient(t *testing.T) {
	t.Parallel()
	if _, _, err := (&graph.Client{}).Documents(context.Background(), nil, "x"); err == nil {
		t.Fatal("want an error")
	}
}

func TestIsSharePointHost(t *testing.T) {
	t.Parallel()
	for host, want := range map[string]bool{"contoso.sharepoint.com": true, "Contoso-my.SharePoint.com": true, "sharepoint.com": false, ".sharepoint.com": false, "evil.com": false} {
		if got := graph.IsSharePointHost(host); got != want {
			t.Errorf("IsSharePointHost(%q) = %v, want %v", host, got, want)
		}
	}
}
