package graph_test

import (
	"context"
	"errors"
	"fmt"
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

// A failed download never puts the pre-authenticated download URL, or its
// tempauth query, in the error: the error reaches stderr and logs, and the
// URL reads the file for about an hour.
func TestDownloadErrorsDoNotLeakTheURL(t *testing.T) {
	for name, setup := range map[string]func(*graphtest.Server){
		"transport failure": func(g *graphtest.Server) { g.FailDownloads = true },
		// The target carries a credential, so the test fails if the
		// redirect's URL reaches the error (#3043).
		"refused redirect": func(g *graphtest.Server) {
			g.Redirect = map[string]string{"download.aspx": "https://evil.example/steal?tempauth=" + graphtest.TempAuth}
		},
		// net/http quotes a Location header it cannot parse (#3043).
		"unparseable Location": func(g *graphtest.Server) {
			g.Redirect = map[string]string{"download.aspx": "https://" + graphtest.SiteHost + "/%zz?tempauth=" + graphtest.TempAuth}
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := graphtest.New(t, graphtest.Item{ID: "1", Name: "A.docx", Content: []byte("a")})
			setup(g)
			_, docs, err := client(t, g).Documents(context.Background(), site(t), "Policies")
			if err == nil || docs != nil {
				t.Fatalf("Documents = %v, %v; want an error and no documents", docs, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, "graph: item 1: download: ") {
				t.Errorf("err = %q; want it to name the item", msg)
			}
			for _, leak := range []string{graphtest.TempAuth, "tempauth", "download.aspx", "UniqueId", "https://" + graphtest.SiteHost} {
				if strings.Contains(msg, leak) {
					t.Errorf("err = %q; leaks %q", msg, leak)
				}
			}
		})
	}
}

func TestDownloadRefusesAPort(t *testing.T) {
	g := graphtest.New(t, graphtest.Item{ID: "1", Name: "A.docx", DownloadURL: "https://contoso.sharepoint.com:8443/f?UniqueId=1&tempauth=" + graphtest.TempAuth})
	_, _, err := client(t, g).Documents(context.Background(), site(t), "Policies")
	if err == nil || !strings.Contains(err.Error(), "with no port") || strings.Contains(err.Error(), graphtest.TempAuth) {
		t.Fatalf("err = %v; want a port refusal without the URL", err)
	}
	for _, r := range g.Requests() {
		if strings.Contains(r.Host, "8443") {
			t.Errorf("requested %s", r.Host)
		}
	}
}

// scrub redacts a secret that survives unwrapping, and leaves an error
// without one as it is.
func TestScrub(t *testing.T) {
	t.Parallel()
	inner := errors.New("dial tcp: lookup x?tempauth=SECRET failed")
	if got := graph.Scrub(&url.Error{Op: "Get", URL: "https://x/?tempauth=SECRET", Err: inner}, "https://x/?tempauth=SECRET", "tempauth=SECRET").Error(); strings.Contains(got, "SECRET") {
		t.Errorf("Scrub = %q", got)
	}
	plain := errors.New("plain")
	if got := graph.Scrub(plain, "", "nothing"); got != plain {
		t.Errorf("Scrub(plain) = %v", got)
	}
}

// A redacted error keeps its chain for errors.Is, and is marked
// ErrRedacted, but nothing reachable by unwrapping it carries the secret
// (#3043).
func TestScrubKeepsTheChain(t *testing.T) {
	t.Parallel()
	raw := "https://x/?tempauth=SECRET"
	inner := fmt.Errorf("read %s: %w", raw, context.DeadlineExceeded)
	got := graph.Scrub(&url.Error{Op: "Get", URL: raw, Err: inner}, raw, "tempauth=SECRET")
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false", got)
	}
	if !errors.Is(got, graph.ErrRedacted) {
		t.Errorf("errors.Is(%v, ErrRedacted) = false", got)
	}
	if errors.Is(got, context.Canceled) {
		t.Errorf("errors.Is(%v, context.Canceled) = true", got)
	}
	for e := got; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), "SECRET") {
			t.Errorf("chain link %q carries the secret", e)
		}
	}
	var ue *url.Error
	if errors.As(got, &ue) {
		t.Errorf("errors.As reaches the *url.Error %v", ue)
	}
}

// A Location header net/http cannot parse is redacted from the error.
func TestScrubLocationHeader(t *testing.T) {
	t.Parallel()
	inner := errors.New(`failed to parse Location header "https://h/%zz?tempauth=OTHER": parse "https://h/%zz?tempauth=OTHER": invalid URL escape "%zz"`)
	got := graph.Scrub(&url.Error{Op: "Get", URL: "https://h/%zz?tempauth=OTHER", Err: inner}, "https://d/?tempauth=SECRET", "tempauth=SECRET").Error()
	if strings.Contains(got, "OTHER") || !strings.Contains(got, "failed to parse Location header") {
		t.Errorf("Scrub = %q; want the Location value redacted and the cause kept", got)
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
