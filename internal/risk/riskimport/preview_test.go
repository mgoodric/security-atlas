package riskimport

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestPreviewFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		csv          string
		opts         ParseOptions
		wantRow      int
		wantDetected bool
		wantCols     []PreviewColumn
		wantRows     [][]string
		wantData     int
	}{
		{
			name:         "aliases detected under a title row",
			csv:          "Acme risk register\nRisk ID,Risk Title,Category,Notes\nR-1,Phishing,Security,a\n,,,\nR-2,Outage,Ops,b\n",
			wantRow:      2,
			wantDetected: true,
			wantCols: []PreviewColumn{
				{Index: 0, Header: "Risk ID", Field: FieldExternalRef, Mappable: true},
				{Index: 1, Header: "Risk Title", Field: FieldTitle, Mappable: true},
				{Index: 2, Header: "Category", Field: FieldCategory, Mappable: true},
				{Index: 3, Header: "Notes", Mappable: true},
			},
			wantRows: [][]string{{"R-1", "Phishing", "Security", "a"}, {"R-2", "Outage", "Ops", "b"}},
			wantData: 2,
		},
		{
			name:    "no alias offers the fullest row",
			csv:     "Register\nWhat,Bucket,Who\nPhishing,Security,a@x\n",
			wantRow: 2,
			wantCols: []PreviewColumn{
				{Index: 0, Header: "What", Mappable: true},
				{Index: 1, Header: "Bucket", Mappable: true},
				{Index: 2, Header: "Who", Mappable: true},
			},
			wantRows: [][]string{{"Phishing", "Security", "a@x"}},
			wantData: 1,
		},
		{
			name:         "column map detects the header and ignore unmaps an alias",
			csv:          "What,Bucket,Owner\nPhishing,Security,a@x\n",
			opts:         ParseOptions{ColumnMap: map[string]string{FieldTitle: "What", FieldCategory: "Bucket"}, Ignore: []string{"owner"}},
			wantRow:      1,
			wantDetected: true,
			wantCols: []PreviewColumn{
				{Index: 0, Header: "What", Field: FieldTitle, Mappable: true},
				{Index: 1, Header: "Bucket", Field: FieldCategory, Mappable: true},
				{Index: 2, Header: "Owner", Mappable: true},
			},
			wantRows: [][]string{{"Phishing", "Security", "a@x"}},
			wantData: 1,
		},
		{
			name:         "blank and repeated headers are not mappable",
			csv:          "Title,Category,Notes,,Notes\nA,B,n1,x,n2\nC,D,,,\n",
			wantRow:      1,
			wantDetected: true,
			wantCols: []PreviewColumn{
				{Index: 0, Header: "Title", Field: FieldTitle, Mappable: true},
				{Index: 1, Header: "Category", Field: FieldCategory, Mappable: true},
				{Index: 2, Header: "Notes", Mappable: true},
				{Index: 3, Header: "column 4"},
				{Index: 4, Header: "Notes"},
			},
			wantRows: [][]string{{"A", "B", "n1", "x", "n2"}, {"C", "D", "", "", ""}},
			wantData: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := PreviewFile("r.csv", []byte(tc.csv), tc.opts)
			if err != nil {
				t.Fatalf("PreviewFile: %v", err)
			}
			if p.HeaderRow != tc.wantRow || p.HeaderDetected != tc.wantDetected {
				t.Errorf("header row %d detected %v, want %d %v", p.HeaderRow, p.HeaderDetected, tc.wantRow, tc.wantDetected)
			}
			if !reflect.DeepEqual(p.Columns, tc.wantCols) {
				t.Errorf("columns = %+v\nwant %+v", p.Columns, tc.wantCols)
			}
			if !reflect.DeepEqual(p.Rows, tc.wantRows) {
				t.Errorf("rows = %q, want %q", p.Rows, tc.wantRows)
			}
			if p.DataRows != tc.wantData {
				t.Errorf("data rows = %d, want %d", p.DataRows, tc.wantData)
			}
		})
	}
}

func TestPreviewFileRowCapAndFields(t *testing.T) {
	t.Parallel()
	csv := "Title,Category\n"
	for i := 0; i < PreviewRows+3; i++ {
		csv += "t,c\n"
	}
	p, err := PreviewFile("r.csv", []byte(csv), ParseOptions{Methodology: dbx.RiskMethodologyFair})
	if err != nil {
		t.Fatalf("PreviewFile: %v", err)
	}
	if len(p.Rows) != PreviewRows || p.DataRows != PreviewRows+3 {
		t.Errorf("rows %d data %d, want %d %d", len(p.Rows), p.DataRows, PreviewRows, PreviewRows+3)
	}
	required := map[string]bool{}
	names := map[string]bool{}
	for _, f := range p.Fields {
		names[f.Name] = true
		if f.Required {
			required[f.Name] = true
		}
	}
	if !reflect.DeepEqual(required, map[string]bool{FieldTitle: true, FieldCategory: true}) {
		t.Errorf("required fields = %v", required)
	}
	if names[FieldInherentLikelihood] || !names[FieldInherentLossMagnitude] {
		t.Errorf("FAIR preview fields = %v, want FAIR score fields only", names)
	}
}

func TestPreviewFileErrors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		file string
		raw  string
		opts ParseOptions
		want error
	}{
		"unsupported":      {"r.txt", "Title,Category\n", ParseOptions{}, ErrUnsupportedFormat},
		"empty":            {"r.csv", "", ParseOptions{}, ErrEmptyFile},
		"blank rows":       {"r.csv", ",,\n,,\n", ParseOptions{}, ErrEmptyFile},
		"mapped + ignored": {"r.csv", "A,B\n", ParseOptions{ColumnMap: map[string]string{FieldTitle: "A"}, Ignore: []string{"a"}}, ErrInvalidColumnMap},
		"bad methodology":  {"r.csv", "A,B\n", ParseOptions{Methodology: "nope"}, ErrInvalidMethodology},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := PreviewFile(tc.file, []byte(tc.raw), tc.opts); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseIgnoreKeepsAliasColumnUnmapped(t *testing.T) {
	t.Parallel()
	res, err := ParseCSVWith([]byte("Title,Category,Owner\nA,B,a@x\n"), ParseOptions{Ignore: []string{"Owner"}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(res.UnmappedColumns, []string{"Owner"}) {
		t.Errorf("unmapped columns = %v, want [Owner]", res.UnmappedColumns)
	}
	if got := res.Risks[0]; got.Owner != "" || got.Unmapped["Owner"] != "a@x" {
		t.Errorf("owner %q unmapped %v, want owner kept as unmapped", got.Owner, got.Unmapped)
	}
}
