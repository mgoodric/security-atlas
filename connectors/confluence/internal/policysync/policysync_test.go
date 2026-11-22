package policysync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/confluence/internal/confluenceapi"
	"github.com/mgoodric/security-atlas/connectors/confluence/internal/storagemd"
)

func TestProperties(t *testing.T) {
	t.Parallel()
	got := Properties([]storagemd.Property{
		{Key: "Version", Value: " 1.0 "},
		{Key: "approved  BY:", Value: "Ada"},
		{Key: "Approval date", Value: "2026-01-01"},
		{Key: "effective_date", Value: "2026-01-02"},
		{Key: "Next review date", Value: ""},
		{Key: "Owner role", Value: "ciso"},
		{Key: "Approver role", Value: "ceo"},
		{Key: "Acknowledgement audience", Value: "all_staff,\nengineering , "},
		{Key: "Version", Value: "9.9"},
		{Key: "Classification", Value: "internal"},
	})
	want := map[string]any{
		"version":                       "1.0",
		"approved_by_name":              "Ada",
		"approved_at":                   "2026-01-01",
		"effective_date":                "2026-01-02",
		"owner_role":                    "ciso",
		"approver_role":                 "ceo",
		"acknowledgment_required_roles": []string{"all_staff", "engineering"},
		"Classification":                "internal",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Properties =\n%#v\nwant\n%#v", got, want)
	}
	if got := Properties([]storagemd.Property{{Key: "Acknowledgment audience", Value: " , "}}); len(got) != 0 {
		t.Errorf("empty audience: %v, want left out", got)
	}
}

func TestBuild(t *testing.T) {
	t.Parallel()
	src := Source{Site: "https://acme.atlassian.net", Space: "POL"}
	req, notes := Build(src, []confluenceapi.Page{
		{ID: "1", Title: "A", URL: "https://acme.atlassian.net/wiki/x/1", Storage: `<ac:structured-macro ac:name="details"><ac:rich-text-body><table><tr><th>Version</th><td>1</td></tr></table></ac:rich-text-body></ac:structured-macro><p>Body</p>`},
		{ID: "2", Title: "B", Storage: `<p>No table</p><ac:structured-macro ac:name="jira" />`},
		{ID: "3", Title: "C", Storage: strings.Repeat("<div>", storagemd.MaxDepth+1)},
	})
	if req.Source != src || len(req.Pages) != 2 {
		t.Fatalf("req = %+v", req)
	}
	if p := req.Pages[0]; p.BodyMd != "Body\n" || !reflect.DeepEqual(p.Properties, map[string]any{"version": "1"}) || p.URL == "" {
		t.Errorf("page 1 = %+v", p)
	}
	if p := req.Pages[1]; p.Properties != nil {
		t.Errorf("page 2 properties = %v, want nil", p.Properties)
	}
	b, _ := json.Marshal(req.Pages[1])
	if !bytes.Contains(b, []byte(`"properties":null`)) {
		t.Errorf("page 2 JSON = %s, want properties null", b)
	}
	if len(notes) != 2 || notes[0].PageID != "2" || notes[0].Unsupported[0].Name != "jira" ||
		notes[1].PageID != "3" || !errors.Is(notes[1].ConvertErr, storagemd.ErrTooDeep) {
		t.Errorf("notes = %+v", notes)
	}
}

func TestPush(t *testing.T) {
	t.Parallel()
	var gotPath, gotAuth string
	var gotReq Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"created":1,"results":[{"page_id":"1","status":"created","policy_id":"p","version":"1"}]}`))
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL + "/atlas/")
	req := Request{Source: Source{Site: "s", Space: "POL"}, Pages: []PagePayload{{ID: "1"}}}
	rep, err := Push(context.Background(), srv.Client(), base, "key", req)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/atlas/v1/policies:sync" || gotAuth != "Bearer key" || gotReq.Pages[0].ID != "1" {
		t.Errorf("request: path %q auth %q body %+v", gotPath, gotAuth, gotReq)
	}
	if rep.Created != 1 || rep.Results[0].PolicyID != "p" {
		t.Errorf("report = %+v", rep)
	}
}

func TestPushFailures(t *testing.T) {
	t.Parallel()
	for name, h := range map[string]http.HandlerFunc{
		"refused": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"policy sync requires a policy-sync API key"}`, http.StatusForbidden)
		},
		"unreadable": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
	} {
		srv := httptest.NewServer(h)
		base, _ := url.Parse(srv.URL)
		if _, err := Push(context.Background(), srv.Client(), base, "k", Request{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
		srv.Close()
	}
	base, _ := url.Parse("http://127.0.0.1:1")
	if _, err := Push(context.Background(), http.DefaultClient, base, "k", Request{}); err == nil {
		t.Error("unreachable: want an error")
	}
	bad := &url.URL{Scheme: "http", Host: "a b"}
	if _, err := Push(context.Background(), http.DefaultClient, bad, "k", Request{}); err == nil {
		t.Error("bad URL: want an error")
	}
}

func TestWriteReport(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	WriteReport(&b, Report{NewVersion: 1, Drift: 1, Missing: 1, Results: []Result{
		{PageID: "1", Title: "A", Status: "new_version", Version: "2", PolicyID: "p2", PredecessorID: "p1"},
		{PageID: "2", Title: "B", Status: "drift", PolicyID: "p3"},
		{PageID: "3", Status: "missing", PolicyID: "p4"},
	}}, []PageNote{
		{PageID: "1", Title: "A", Unsupported: []storagemd.Unsupported{{Name: "toc", Count: 2}, {Name: "ac:image", Count: 1}}},
		{PageID: "9", Title: "Z", ConvertErr: storagemd.ErrBodyTooLarge},
	})
	want := `page 1 "A": new_version version=2 policy=p2 predecessor=p1
page 2 "B": drift policy=p3
page 3 "": missing policy=p4
page 1 "A": unsupported macros: ac:image x1, toc x2
page 9 "Z": skipped by connector reason=body not converted: storagemd: body too large
sync: created=0 new_version=1 taken_over=0 unchanged=0 drift=1 skipped=0 missing=1 skipped_by_connector=1
`
	if b.String() != want {
		t.Errorf("report =\n%s\nwant\n%s", b.String(), want)
	}
}
