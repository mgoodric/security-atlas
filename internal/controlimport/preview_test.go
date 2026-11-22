package controlimport

import (
	"errors"
	"reflect"
	"testing"
)

func TestPreviewFile_DetectsAliasHeaders(t *testing.T) {
	t.Parallel()
	raw := []byte("Control ID,Title,Description,Owner\nAC-1,Access,Users are reviewed,ann\n\nAC-2,MFA,MFA everywhere,bob\n")
	p, err := PreviewFile("c.csv", raw, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HeaderDetected || p.HeaderRow != 1 {
		t.Fatalf("header = row %d detected %v, want row 1 detected", p.HeaderRow, p.HeaderDetected)
	}
	got := map[string]string{}
	for _, c := range p.Columns {
		got[c.Header] = c.Field
	}
	want := map[string]string{"Control ID": FieldID, "Title": FieldTitle, "Description": FieldDescription, "Owner": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	if p.DataRows != 2 || len(p.Rows) != 2 || p.Rows[1][0] != "AC-2" {
		t.Fatalf("rows = %d %v", p.DataRows, p.Rows)
	}
	var required []string
	for _, f := range p.Fields {
		if f.Required {
			required = append(required, f.Name)
		}
	}
	if !reflect.DeepEqual(required, []string{FieldID, FieldTitle, FieldDescription}) || len(p.Fields) != 6 {
		t.Fatalf("fields = %+v", p.Fields)
	}
}

// A sheet whose headers match no alias is offered by its fullest early row,
// and the column map then maps it the way the upload will read it.
func TestPreviewFile_NoAliasHeadersMapByHand(t *testing.T) {
	t.Parallel()
	raw := []byte("Our controls\nKey,Headline,Body,Kind\nX-1,Backups,Nightly backups,manual\n")
	p, err := PreviewFile("c.csv", raw, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.HeaderDetected || p.HeaderRow != 2 {
		t.Fatalf("header = row %d detected %v, want row 2 offered", p.HeaderRow, p.HeaderDetected)
	}
	for _, c := range p.Columns {
		if c.Field != "" || !c.Mappable {
			t.Fatalf("column %+v should be unmapped and mappable", c)
		}
	}
	if _, err := Parse("c.csv", raw, ParseOptions{}); !errors.Is(err, ErrNoHeaderRow) {
		t.Fatalf("Parse without a map err = %v, want ErrNoHeaderRow", err)
	}

	opts := ParseOptions{ColumnMap: map[string]string{FieldID: "Key", FieldTitle: "Headline", FieldDescription: "Body"}}
	p, err = PreviewFile("c.csv", raw, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HeaderDetected || p.Columns[0].Field != FieldID || p.Columns[3].Field != "" {
		t.Fatalf("mapped preview = %+v", p)
	}
	res, err := Parse("c.csv", raw, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Controls) != 1 || res.Controls[0].ExternalID != "X-1" || res.Controls[0].Title != "Backups" {
		t.Fatalf("parsed = %+v", res)
	}
	if !reflect.DeepEqual(res.UnmappedColumns, []string{"Kind"}) {
		t.Fatalf("unmapped = %v", res.UnmappedColumns)
	}
}

func TestPreviewFile_BlankAndRepeatedHeadersNotMappable(t *testing.T) {
	t.Parallel()
	raw := []byte("ID,Title,Description,,Title,\nA,B,C,x,D,\n")
	p, err := PreviewFile("c.csv", raw, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Columns) != 5 {
		t.Fatalf("columns = %+v, want the empty blank column dropped", p.Columns)
	}
	if c := p.Columns[3]; c.Header != "column 4" || c.Mappable {
		t.Fatalf("blank column = %+v", c)
	}
	if c := p.Columns[4]; c.Field != "" || c.Mappable {
		t.Fatalf("repeated Title = %+v, want unmapped and not mappable", c)
	}
}

func TestPreviewFile_Errors(t *testing.T) {
	t.Parallel()
	if _, err := PreviewFile("c.txt", []byte("a"), ParseOptions{}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v", err)
	}
	if _, err := PreviewFile("c.csv", nil, ParseOptions{}); !errors.Is(err, ErrEmptyFile) {
		t.Fatalf("err = %v", err)
	}
	if _, err := PreviewFile("c.csv", []byte("\n,\n"), ParseOptions{}); !errors.Is(err, ErrEmptyFile) {
		t.Fatalf("all-blank err = %v", err)
	}
}

func TestParse_IgnoreKeepsAliasColumnUnmapped(t *testing.T) {
	t.Parallel()
	raw := []byte("ID,Title,Description,Type\nA,B,C,not-a-type\n")
	if res, err := Parse("c.csv", raw, ParseOptions{}); err != nil || len(res.Rejected) != 1 {
		t.Fatalf("without ignore: %+v %v, want the type rejected", res, err)
	}
	res, err := Parse("c.csv", raw, ParseOptions{Ignore: []string{"type"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Controls) != 1 || res.Controls[0].ImplementationType != "" {
		t.Fatalf("controls = %+v", res.Controls)
	}
	if !reflect.DeepEqual(res.UnmappedColumns, []string{"Type"}) {
		t.Fatalf("unmapped = %v", res.UnmappedColumns)
	}
}

func TestParse_InvalidIgnoreAndMap(t *testing.T) {
	t.Parallel()
	raw := []byte("ID,Title,Description\nA,B,C\n")
	cases := map[string]ParseOptions{
		"mapped and ignored": {ColumnMap: map[string]string{FieldTitle: "Title"}, Ignore: []string{"title"}},
		"empty ignore":       {Ignore: []string{" "}},
		"header mapped twice": {ColumnMap: map[string]string{
			FieldTitle: "Title", FieldDescription: "title",
		}},
	}
	for name, opts := range cases {
		if _, err := Parse("c.csv", raw, opts); !errors.Is(err, ErrInvalidColumnMap) {
			t.Errorf("%s: err = %v, want ErrInvalidColumnMap", name, err)
		}
		if _, err := PreviewFile("c.csv", raw, opts); !errors.Is(err, ErrInvalidColumnMap) {
			t.Errorf("%s: preview err = %v, want ErrInvalidColumnMap", name, err)
		}
	}
}
