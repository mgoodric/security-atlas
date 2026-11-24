package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/shared/policysync"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/graph/graphtest"
)

// docx builds a minimal Word document whose body is the given paragraphs.
func docx(t *testing.T, paras ...string) []byte {
	t.Helper()
	var body strings.Builder
	for _, p := range paras {
		body.WriteString(`<w:p><w:r><w:t>` + p + `</w:t></w:r></w:p>`)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"_rels/.rels":       `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `</w:body></w:document>`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// policyFields are a complete set of policy columns, keyed as Graph
// returns a library's column internal names.
func policyFields(title, version string) map[string]any {
	return map[string]any{
		"Title":                          title,
		"FileLeafRef":                    title + ".docx",
		"ContentType":                    "Document",
		"_UIVersionString":               "7.0",
		"Policy_x0020_version":           version,
		"Approved_x0020_by":              "Jane Doe",
		"Approval_x0020_date":            "2025-02-28T23:00:00Z",
		"Effective_x0020_date":           "2025-03-14T23:00:00Z",
		"Acknowledgment_x0020_audience":  []any{"everyone"},
		"Modified":                       "2026-11-01T10:00:00Z",
		"Owner_x0020_role":               "",
		"ApproverLookupId":               "12",
		"Approver_x0020_role":            map[string]any{"LookupId": 7, "LookupValue": "CISO"},
		"Next_x0020_review_x0020_date":   nil,
		"Created":                        "2025-01-01T10:00:00Z",
		"_ComplianceTag":                 "",
		"LinkFilenameNoMenu":             title + ".docx",
		"DocIcon":                        "docx",
		"FileSizeDisplay":                "12345",
		"ItemChildCount":                 "0",
		"FolderChildCount":               "0",
		"_CommentCount":                  "",
		"_LikeCount":                     "",
		"Edit":                           "0",
		"ParentVersionStringLookupId":    "1",
		"ParentLeafNameLookupId":         "1",
		"AppAuthorLookupId":              "1",
		"AppEditorLookupId":              "1",
		"AuthorLookupId":                 "6",
		"EditorLookupId":                 "6",
		"_CheckinComment":                "",
		"CheckoutUserLookupId":           nil,
		"_DisplayName":                   "",
		"Owner":                          map[string]any{"LookupId": 6, "LookupValue": "Jane Doe"},
		"Policy_x0020_version_x0020_old": "0.9",
	}
}

// fullLibrary is a library of five items over three pages: two policies,
// a folder, a PDF and a .docx that is not a zip.
func fullLibrary(t *testing.T) *graphtest.Server {
	t.Helper()
	missing := policyFields("Vendor Policy", "1.0")
	delete(missing, "Effective_x0020_date")
	g := graphtest.New(t,
		graphtest.Item{ID: "1", Name: "Access Control Policy.docx", Fields: policyFields("Access Control Policy", "2.0"), Content: docx(t, "Access is granted by role.", "# reviewed quarterly")},
		graphtest.Item{ID: "2", Name: "Archive", Folder: true, Fields: map[string]any{"Title": "Archive", "ContentType": "Folder"}},
		graphtest.Item{ID: "3", Name: "Vendor Policy.docx", Fields: missing, Content: docx(t, "Vendors are reviewed.")},
		graphtest.Item{ID: "4", Name: "Old Policy.pdf", Fields: map[string]any{"Title": ""}},
		graphtest.Item{ID: "5", Name: "Broken.docx", Fields: map[string]any{}, Content: []byte("not a zip")},
	)
	g.ItemsPerPage = 2
	return g
}

func testConfig(g *graphtest.Server) config {
	return config{
		site:     "https://" + graphtest.SiteHost + graphtest.SitePath,
		library:  "policies",
		tenantID: graphtest.Tenant,
		clientID: graphtest.ClientID,
		timeZone: "Europe/Berlin",
		atlasURL: g.URL,
		atlasKey: "policy-sync-key",
		timeout:  time.Minute,
	}
}

func resolved(t *testing.T, c config) config {
	t.Helper()
	if err := c.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return c
}

type pushed struct {
	Source policysync.Source        `json:"source"`
	Pages  []policysync.PagePayload `json:"pages"`
}

// acceptSync answers the push with the given report and records the
// request.
func acceptSync(t *testing.T, g *graphtest.Server, got *pushed, rep policysync.Report) {
	g.Sync = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer policy-sync-key" {
			t.Errorf("sync Authorization = %q", r.Header.Get("Authorization"))
		}
		graphtest.ReadBody(t, r, got)
		_ = json.NewEncoder(w).Encode(rep)
	}
}

func TestRun_FullLibrary(t *testing.T) {
	t.Setenv("SHAREPOINT_CLIENT_SECRET", graphtest.ClientSecret)
	g := fullLibrary(t)
	var got pushed
	acceptSync(t, g, &got, policysync.Report{
		Created: 1, Skipped: 1, Unreadable: 2,
		Results: []policysync.Result{
			{PageID: "1", Title: "Access Control Policy", Status: "created", Version: "2.0", PolicyID: "p1"},
			{PageID: "3", Title: "Vendor Policy", Status: "skipped", Reason: "effective_date is required"},
			{PageID: "4", Title: "Old Policy", Status: "unreadable", Reason: "body not converted: not a .docx document (.pdf); only Word .docx files convert"},
			{PageID: "5", Title: "Broken", Status: "unreadable", Reason: "body not converted: docxmd: not a .docx document: zip: not a valid zip file"},
		},
	})
	var out bytes.Buffer
	err := run(context.Background(), resolved(t, testConfig(g)), g.Client(), &out)

	// Two documents were sent unreadable, more than --max-skipped=0.
	if exitCode(err) != ExitNeedsAttention {
		t.Fatalf("run err = %v (exit %d), want exit %d", err, exitCode(err), ExitNeedsAttention)
	}
	if got.Source != (policysync.Source{Site: "https://contoso.sharepoint.com/sites/policies", Space: "Policies"}) {
		t.Errorf("source = %+v", got.Source)
	}
	if len(got.Pages) != 4 {
		t.Fatalf("pushed %d documents, want 4 (the folder left out): %+v", len(got.Pages), got.Pages)
	}
	access := got.Pages[0]
	if access.ID != "1" || access.Title != "Access Control Policy" ||
		access.URL != "https://contoso.sharepoint.com/sites/Policies/Policies/Access%20Control%20Policy.docx" {
		t.Errorf("access page = %+v", access)
	}
	if access.BodyMd != "Access is granted by role.\n\n\\# reviewed quarterly" {
		t.Errorf("access body = %q", access.BodyMd)
	}
	wantProps := map[string]any{
		"version":                       "2.0",
		"approved_by_name":              "Jane Doe",
		"approved_at":                   "2025-03-01", // 23:00Z is midnight in Berlin
		"effective_date":                "2025-03-15",
		"acknowledgment_required_roles": []any{"everyone"},
	}
	if !jsonEqual(t, access.Properties, wantProps) {
		t.Errorf("access properties = %v, want %v", access.Properties, wantProps)
	}
	if _, ok := got.Pages[1].Properties["effective_date"]; ok || got.Pages[1].BodyMd == "" {
		t.Errorf("vendor page = %+v; want a body and no effective_date", got.Pages[1])
	}
	for _, p := range got.Pages[2:] {
		if !p.Unreadable || p.BodyMd != "" || p.Properties != nil || !strings.HasPrefix(p.UnreadableReason, "body not converted: ") {
			t.Errorf("page %s = %+v; want unreadable with a reason and nothing else", p.ID, p)
		}
	}
	if !strings.Contains(got.Pages[2].UnreadableReason, "not a .docx document (.pdf)") {
		t.Errorf("pdf reason = %q", got.Pages[2].UnreadableReason)
	}

	report := out.String()
	for _, want := range []string{
		`document 1 "Access Control Policy": created version=2.0 policy=p1`,
		`document 3 "Vendor Policy": skipped reason=effective_date is required`,
		`document 1 "Access Control Policy": columns not read (person, lookup or other unsupported type): Approver_x0020_role`,
		"sync: created=1 new_version=0 taken_over=0 unchanged=0 drift=0 skipped=1 missing=0 unreadable=2",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}

	// Where each credential went: the secret only to the token endpoint,
	// the bearer token only to Graph, nothing to the download host.
	for _, r := range g.Requests() {
		switch host := strings.ToLower(r.Host); {
		case host == "login.microsoftonline.com":
			if r.Form.Get("client_secret") != graphtest.ClientSecret || r.Authorization != "" {
				t.Errorf("token request = %+v", r)
			}
		case host == "graph.microsoft.com":
			if r.Method != http.MethodGet || r.Authorization != "Bearer "+graphtest.Token {
				t.Errorf("graph request %s %s: Authorization %q", r.Method, r.Path, r.Authorization)
			}
		case host == "contoso.sharepoint.com":
			if r.Authorization != "" {
				t.Errorf("download carried Authorization %q", r.Authorization)
			}
		case strings.HasPrefix(host, "127.0.0.1:"):
			if r.Path != "/v1/policies:sync" {
				t.Errorf("platform request to %s", r.Path)
			}
		default:
			t.Errorf("request to unexpected host %q", r.Host)
		}
		if r.Form.Get("client_secret") != "" && r.Host != "login.microsoftonline.com" {
			t.Errorf("client secret sent to %s", r.Host)
		}
	}

	// Within --max-skipped the same run exits 0.
	c := resolved(t, testConfig(g))
	c.maxSkipped = 2
	if err := run(context.Background(), c, g.Client(), &bytes.Buffer{}); err != nil {
		t.Fatalf("run with --max-skipped=2: %v", err)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

// Nothing is pushed unless the whole library was read.
func TestRun_NothingPushed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*graphtest.Server)
		want  string
	}{
		{"token refused", func(g *graphtest.Server) { g.Fail = map[string]int{"/oauth2/": http.StatusUnauthorized} }, "graph: token: HTTP 401"},
		{"site not found", func(g *graphtest.Server) { g.Fail = map[string]int{":/sites/": http.StatusNotFound} }, "graph: site: HTTP 404"},
		{"items fail mid-read", func(g *graphtest.Server) { g.Fail = map[string]int{"/items/3/": http.StatusServiceUnavailable} }, "graph: item 3: HTTP 503"},
		{"download fails", func(g *graphtest.Server) { g.Fail = map[string]int{"download.aspx": http.StatusForbidden} }, "graph: item 1: download: HTTP 403"},
		{"next link off Graph", func(g *graphtest.Server) {
			g.NextLink = "https://evil.example/v1.0/sites/x/lists/y/items?$skiptoken=2"
		}, "refused a URL off https://graph.microsoft.com"},
		{"next link over http", func(g *graphtest.Server) {
			g.NextLink = "http://graph.microsoft.com/v1.0/sites/x/lists/y/items?$skiptoken=2"
		}, "refused a URL off https://graph.microsoft.com"},
		{"redirect off Graph", func(g *graphtest.Server) {
			g.Redirect = map[string]string{"/driveItem": "https://evil.example/steal"}
		}, "refused redirect"},
		{"download off SharePoint", func(g *graphtest.Server) { g.Items[0].DownloadURL = "https://evil.example/f?UniqueId=1" }, "download URL is not https on a .sharepoint.com host"},
		{"download over http", func(g *graphtest.Server) {
			g.Items[0].DownloadURL = "http://contoso.sharepoint.com/f?UniqueId=1"
		}, "download URL is not https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHAREPOINT_CLIENT_SECRET", graphtest.ClientSecret)
			g := fullLibrary(t)
			tc.setup(g)
			pushedAny := false
			g.Sync = func(http.ResponseWriter, *http.Request) { pushedAny = true }
			err := run(context.Background(), resolved(t, testConfig(g)), g.Client(), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || exitCode(err) != 1 {
				t.Fatalf("run err = %v, want exit 1 containing %q", err, tc.want)
			}
			if pushedAny {
				t.Fatal("pushed after a failed read")
			}
			for _, r := range g.Requests() {
				if strings.EqualFold(r.Host, "evil.example") && (r.Authorization != "" || r.Form != nil) {
					t.Errorf("credential sent to evil.example: %+v", r)
				}
			}
		})
	}
}

func TestRun_EmptyAndUnconvertible(t *testing.T) {
	t.Setenv("SHAREPOINT_CLIENT_SECRET", graphtest.ClientSecret)

	// Only a folder: no documents, nothing pushed.
	g := graphtest.New(t, graphtest.Item{ID: "1", Name: "Archive", Folder: true})
	g.Sync = func(http.ResponseWriter, *http.Request) { t.Error("pushed an empty library") }
	err := run(context.Background(), resolved(t, testConfig(g)), g.Client(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `no documents in library "policies"`) {
		t.Fatalf("empty: err = %v", err)
	}

	// Nothing convertible: nothing pushed, the reasons printed.
	g = graphtest.New(t, graphtest.Item{ID: "4", Name: "Old Policy.pdf", Fields: map[string]any{}})
	g.Sync = func(http.ResponseWriter, *http.Request) { t.Error("pushed an unconvertible library") }
	var out bytes.Buffer
	err = run(context.Background(), resolved(t, testConfig(g)), g.Client(), &out)
	if err == nil || !strings.Contains(err.Error(), "no document could be converted") {
		t.Fatalf("unconvertible: err = %v", err)
	}
	if !strings.Contains(out.String(), "sync: created=0") {
		t.Errorf("report = %q", out.String())
	}
}

func TestRun_PlatformAnswers(t *testing.T) {
	t.Setenv("SHAREPOINT_CLIENT_SECRET", graphtest.ClientSecret)
	g := graphtest.New(t, graphtest.Item{ID: "1", Name: "A.docx", Fields: policyFields("A", "1.0"), Content: docx(t, "Body.")})

	g.Sync = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"policy sync requires a policy-sync API key"}`))
	}
	err := run(context.Background(), resolved(t, testConfig(g)), g.Client(), &bytes.Buffer{})
	var pe *policysync.PushError
	if !errors.As(err, &pe) || pe.Status != http.StatusForbidden || exitCode(err) != 1 {
		t.Fatalf("refused push: err = %v", err)
	}

	var got pushed
	acceptSync(t, g, &got, policysync.Report{Unchanged: 1, Warnings: []string{"more than half of this source's policies are missing"}})
	var out bytes.Buffer
	err = run(context.Background(), resolved(t, testConfig(g)), g.Client(), &out)
	if exitCode(err) != ExitNeedsAttention || !strings.Contains(err.Error(), "1 platform warning(s)") {
		t.Fatalf("warning: err = %v", err)
	}
	if !strings.Contains(out.String(), "warning: more than half") {
		t.Errorf("report = %q", out.String())
	}
}

func TestResolve(t *testing.T) {
	t.Setenv(EnvSite, "")
	t.Setenv(EnvLibrary, "")
	t.Setenv(EnvAtlasURL, "")
	t.Setenv(EnvAtlasKey, "")
	t.Setenv(EnvTimeZone, "")
	c := config{timeout: time.Minute}
	err := c.resolve()
	if err == nil || err.Error() != "required: --site (env SHAREPOINT_SITE), --library (env SHAREPOINT_LIBRARY), --atlas-url (env SECURITY_ATLAS_URL), env SECURITY_ATLAS_POLICY_SYNC_KEY" {
		t.Fatalf("resolve() = %v", err)
	}

	t.Setenv(EnvSite, "https://contoso.sharepoint.com/sites/Policies")
	t.Setenv(EnvLibrary, " Policies ")
	t.Setenv(EnvAtlasURL, "https://atlas.example.com")
	t.Setenv(EnvAtlasKey, "k")
	c = config{timeout: time.Minute}
	if err := c.resolve(); err != nil || c.library != "Policies" || c.loc != time.UTC {
		t.Fatalf("resolve from env = %v, %+v", err, c)
	}
	for _, bad := range []config{
		{timeZone: "Mars/Olympus", timeout: time.Minute},
		{timeout: 0},
		{timeout: time.Minute, maxSkipped: -1},
	} {
		if err := bad.resolve(); err == nil {
			t.Errorf("resolve(%+v): want an error", bad)
		}
	}
}

func TestRun_BadConfig(t *testing.T) {
	t.Setenv("SHAREPOINT_CLIENT_SECRET", graphtest.ClientSecret)
	g := graphtest.New(t)
	for name, mut := range map[string]func(*config){
		"site off SharePoint": func(c *config) { c.site = "https://acme.atlassian.net/wiki" },
		"site over http":      func(c *config) { c.site = "http://contoso.sharepoint.com/sites/Policies" },
		"site with no path":   func(c *config) { c.site = "https://contoso.sharepoint.com" },
		"platform over http":  func(c *config) { c.atlasURL = "http://atlas.example.com" },
		"bad tenant":          func(c *config) { c.tenantID = "../evil" },
	} {
		c := resolved(t, testConfig(g))
		mut(&c)
		if err := run(context.Background(), c, g.Client(), &bytes.Buffer{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if len(g.Requests()) != 0 {
		t.Errorf("bad config still made %d requests", len(g.Requests()))
	}
	t.Setenv("SHAREPOINT_CLIENT_SECRET", "")
	if err := run(context.Background(), resolved(t, testConfig(g)), g.Client(), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "SHAREPOINT_CLIENT_SECRET") {
		t.Errorf("no secret: err = %v", err)
	}
}

func TestCommand(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd(&out)
	root.SetArgs([]string{"run", "--timeout", "0s"})
	root.SetOut(&out)
	root.SetErr(&out)
	t.Setenv(EnvSite, "https://contoso.sharepoint.com/sites/Policies")
	t.Setenv(EnvLibrary, "Policies")
	t.Setenv(EnvAtlasURL, "https://atlas.example.com")
	t.Setenv(EnvAtlasKey, "k")
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--timeout must be positive") {
		t.Fatalf("Execute = %v", err)
	}
	if exitCode(errors.New("x")) != 1 {
		t.Error("exitCode(plain) != 1")
	}
}
