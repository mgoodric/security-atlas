package docsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/shared/policysync"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/docxmd"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/graph"
)

func TestColumnKey(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Approved_x0020_by":            "approvedby",
		"Approvedby":                   "approvedby",
		"Next_x0020_review_x0020_date": "nextreviewdate",
		"Approved by:":                 "approvedby",
		" Policy  Version ":            "policyversion",
		"approved_by_name":             "approved_by_name",
		"_UIVersionString":             "_uiversionstring",
		"Bad_xZZZZ_code":               "bad_xzzzz_code",
		"Ack_x00e9_":                   "acké",
	} {
		if got := columnKey(in); got != want {
			t.Errorf("columnKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProperties(t *testing.T) {
	t.Parallel()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	props, skipped := Properties(map[string]any{
		"Policy_x0020_version":          2.0,
		"Version":                       "9.9", // a second version column: the first by name wins
		"Approved_x0020_by":             " Jane Doe ",
		"ApprovalDate":                  "2025-02-28T23:00:00Z",
		"Effective_x0020_date":          "2025-03-15",
		"Next_x0020_review_x0020_date":  "not a date",
		"OwnerRole":                     "",
		"Approver_x0020_role":           map[string]any{"LookupId": 7},
		"Acknowledgment_x0020_audience": "everyone; admin,\nauditor",
		"Title":                         "Ignored",
		"_UIVersionString":              "3.0",
		"Flag":                          true,
	}, berlin)
	want := map[string]any{
		"version":                       "2",
		"approved_by_name":              "Jane Doe",
		"approved_at":                   "2025-03-01",
		"effective_date":                "2025-03-15",
		"next_review_at":                "not a date",
		"acknowledgment_required_roles": []string{"everyone", "admin", "auditor"},
	}
	if !reflect.DeepEqual(props, want) {
		t.Errorf("Properties = %#v, want %#v", props, want)
	}
	if !reflect.DeepEqual(skipped, []string{"Approver_x0020_role"}) {
		t.Errorf("skipped = %v", skipped)
	}

	props, skipped = Properties(map[string]any{
		"Acknowledgment_x0020_audience": []any{"everyone", " ", 3.0},
		"Owner_x0020_role":              nil,
	}, time.UTC)
	if len(props) != 0 || !reflect.DeepEqual(skipped, []string{"Acknowledgment_x0020_audience"}) {
		t.Errorf("bad audience: props %v skipped %v", props, skipped)
	}
	props, _ = Properties(map[string]any{"acknowledgment_required_roles": []any{" "}, "AcknowledgementAudience": nil}, time.UTC)
	if len(props) != 0 {
		t.Errorf("empty audience: props %v", props)
	}
	props, skipped = Properties(map[string]any{"acknowledgment_required_roles": 3.0}, time.UTC)
	if len(props) != 0 || len(skipped) != 1 {
		t.Errorf("numeric audience: props %v skipped %v", props, skipped)
	}
}

func TestTitle(t *testing.T) {
	t.Parallel()
	if got := Title(graph.Document{Name: "Access Policy.docx", Fields: map[string]any{"Title": " Access Control "}}); got != "Access Control" {
		t.Errorf("Title = %q", got)
	}
	if got := Title(graph.Document{Name: "Access Policy.v2.docx", Fields: map[string]any{"Title": ""}}); got != "Access Policy.v2" {
		t.Errorf("Title = %q", got)
	}
}

func TestBuildAndReport(t *testing.T) {
	t.Parallel()
	src := policysync.Source{Site: "https://contoso.sharepoint.com/sites/policies", Space: "Policies"}
	docs := []graph.Document{
		{ID: "1", Name: "Image.docx", WebURL: "https://contoso.sharepoint.com/a", Fields: map[string]any{"Policy_x0020_version": "1.0", "Approver_x0020_role": map[string]any{}}, Content: imageDocx(t)},
		{ID: "2", Name: "README", WebURL: "https://contoso.sharepoint.com/b"},
		{ID: "3", Name: "Huge.docx", Content: make([]byte, docxmd.MaxFileBytes+1)},
	}
	req, notes := Build(src, docs, time.UTC)
	if req.Source != src || len(req.Pages) != 3 || req.Readable() != 1 {
		t.Fatalf("request = %+v", req)
	}
	if got := req.Pages[0]; got.BodyMd == "" || got.Properties["version"] != "1.0" || got.Unreadable {
		t.Errorf("page 1 = %+v", got)
	}
	if got := req.Pages[1]; !got.Unreadable || got.UnreadableReason != "body not converted: not a .docx document (no extension); only Word .docx files convert" {
		t.Errorf("page 2 = %+v", got)
	}
	if got := req.Pages[2]; !got.Unreadable || !strings.Contains(got.UnreadableReason, "document too large") {
		t.Errorf("page 3 = %+v", got)
	}
	if ConverterSkipped(notes) != 2 {
		t.Errorf("ConverterSkipped = %d", ConverterSkipped(notes))
	}
	if !errors.Is(notes[2].ConvertErr, docxmd.ErrTooLarge) {
		t.Errorf("note 3 err = %v", notes[2].ConvertErr)
	}
	// Unreadable pages carry no properties at all (JSON null), as the
	// platform requires.
	b, _ := json.Marshal(req.Pages[1])
	if !strings.Contains(string(b), `"properties":null`) {
		t.Errorf("unreadable page JSON = %s", b)
	}

	var out bytes.Buffer
	WriteReport(&out, policysync.Report{Created: 1, Results: []policysync.Result{{PageID: "1", Title: "Image", Status: "created"}}}, notes)
	want := `document 1 "Image": created
document 1 "Image": unsupported content: image x1
document 1 "Image": columns not read (person, lookup or other unsupported type): Approver_x0020_role
sync: created=1 new_version=0 taken_over=0 unchanged=0 drift=0 skipped=0 missing=0 unreadable=0
`
	if out.String() != want {
		t.Errorf("report =\n%s\nwant\n%s", out.String(), want)
	}
}

// imageDocx is a .docx with one paragraph holding a picture.
func imageDocx(t *testing.T) []byte {
	t.Helper()
	return zipDoc(t, `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Diagram: </w:t></w:r><w:r><w:drawing/></w:r></w:p></w:body></w:document>`)
}
