package riskimport

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/risk"
)

// rowSummary is what a parse test asserts of a staged row.
type rowSummary struct {
	Row                        int
	Ref, Title, Desc, Cat, Due string
	Unmapped                   map[string]string
}

func summarize(rs []StagedRisk) []rowSummary {
	out := make([]rowSummary, 0, len(rs))
	for _, r := range rs {
		s := rowSummary{Row: r.RowNumber, Ref: r.ExternalRef, Title: r.Title, Desc: r.Description, Cat: r.Category, Unmapped: r.Unmapped}
		if r.ReviewDue != nil {
			s.Due = r.ReviewDue.Format("2006-01-02")
		}
		out = append(out, s)
	}
	return out
}

func rejections(rs []RejectedRow) map[int]string {
	out := map[int]string{}
	for _, r := range rs {
		out[r.RowNumber] = r.Reason
	}
	return out
}

func TestParseCSV(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		csv          string
		columnMap    map[string]string
		wantRisks    []rowSummary
		wantRejected map[int]string
		wantUnmapped []string
	}{
		{
			name: "alias headers",
			csv:  "Risk ID,Risk Title,Risk Description,Risk Category,Next Review Date\nR-1,Outage,Vendor down,operational,2027-02-01\n",
			wantRisks: []rowSummary{{Row: 2, Ref: "R-1", Title: "Outage", Desc: "Vendor down", Cat: "operational", Due: "2027-02-01",
				Unmapped: map[string]string{}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{},
		},
		{
			name: "a title row above the header",
			csv:  "Risk register,,\nexported 2026-11-01,,\nTitle,Category,Ref\nOutage,operational,R-1\n",
			wantRisks: []rowSummary{{Row: 4, Ref: "R-1", Title: "Outage", Cat: "operational",
				Unmapped: map[string]string{}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{},
		},
		{
			name: "the risk export's own headers re-import without a map",
			csv: "id,title,description,category,treatment,review_due_at,category_name,external_ref\n" +
				"6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839,Outage,Vendor down,operational,untreated,2027-02-01T00:00:00Z,Operational,R-1\n",
			wantRisks: []rowSummary{{Row: 2, Ref: "R-1", Title: "Outage", Desc: "Vendor down", Cat: "operational", Due: "2027-02-01",
				Unmapped: map[string]string{"id": "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839", "treatment": "untreated", "category_name": "Operational"}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{"id", "treatment", "category_name"},
		},
		{
			name:      "a column map overrides alias detection",
			csv:       "Name,Headline,Bucket,Ref No.\nignored,Mapped title,privacy,X-1\n",
			columnMap: map[string]string{FieldTitle: "Headline", FieldCategory: "Bucket", FieldExternalRef: "Ref No."},
			wantRisks: []rowSummary{{Row: 2, Ref: "X-1", Title: "Mapped title", Cat: "privacy",
				Unmapped: map[string]string{"Name": "ignored"}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{"Name"},
		},
		{
			name: "unmapped columns are kept and never reach the description",
			csv:  "Title,Category,Description,Owner,,Target Date\nOutage,operational,Vendor down,alice@corp.test,stray,2027-03-01\nSecond,operational,,,,\n",
			wantRisks: []rowSummary{
				{Row: 2, Title: "Outage", Desc: "Vendor down", Cat: "operational",
					Unmapped: map[string]string{"Owner": "alice@corp.test", "column 5": "stray", "Target Date": "2027-03-01"}},
				{Row: 3, Title: "Second", Cat: "operational", Unmapped: map[string]string{}},
			},
			wantRejected: map[int]string{},
			wantUnmapped: []string{"Owner", "column 5", "Target Date"},
		},
		{
			name: "a repeated risk ID in the file is rejected, ignoring case",
			csv:  "Risk ID,Title,Category\nR-1,First,operational\nr-1,Second,operational\n,No ID,operational\n,Also no ID,operational\n",
			wantRisks: []rowSummary{
				{Row: 2, Ref: "R-1", Title: "First", Cat: "operational", Unmapped: map[string]string{}},
				{Row: 4, Title: "No ID", Cat: "operational", Unmapped: map[string]string{}},
				{Row: 5, Title: "Also no ID", Cat: "operational", Unmapped: map[string]string{}},
			},
			wantRejected: map[int]string{3: RejectDuplicateExternal},
			wantUnmapped: []string{},
		},
		{
			name: "row checks",
			csv: "Risk ID,Title,Category,Review Due\n" +
				"R-1,,operational,\n" +
				"R-2,No category,,\n" +
				"R-3,Bad date,operational,soon\n" +
				strings.Repeat("x", risk.MaxExternalRefLen+1) + ",Long ID,operational,\n" +
				"R-1,Not a duplicate of a rejected row,operational,\n" +
				",,,\n",
			wantRisks: []rowSummary{{Row: 6, Ref: "R-1", Title: "Not a duplicate of a rejected row", Cat: "operational",
				Unmapped: map[string]string{}}},
			wantRejected: map[int]string{
				2: RejectMissingField, 3: RejectMissingField, 4: RejectInvalidReviewDue, 5: RejectInvalidExternalRef,
			},
			wantUnmapped: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCSVWith([]byte(tc.csv), ParseOptions{ColumnMap: tc.columnMap})
			if err != nil {
				t.Fatalf("ParseCSVWith: %v", err)
			}
			if s := summarize(got.Risks); !reflect.DeepEqual(s, tc.wantRisks) {
				t.Errorf("risks =\n%+v\nwant\n%+v", s, tc.wantRisks)
			}
			if r := rejections(got.Rejected); !reflect.DeepEqual(r, tc.wantRejected) {
				t.Errorf("rejected = %v, want %v (%+v)", r, tc.wantRejected, got.Rejected)
			}
			if !reflect.DeepEqual(got.UnmappedColumns, tc.wantUnmapped) {
				t.Errorf("unmapped columns = %q, want %q", got.UnmappedColumns, tc.wantUnmapped)
			}
		})
	}
}

func TestParseCSVErrors(t *testing.T) {
	t.Parallel()
	manyRows := "Title,Category\n" + strings.Repeat("Risk,operational\n", MaxRows)
	wide := "Title,Category" + strings.Repeat(",x", MaxColumns-1) + "\n"
	cases := []struct {
		name      string
		raw       []byte
		columnMap map[string]string
		want      error
	}{
		{"empty", nil, nil, ErrEmptyFile},
		{"too large", bytes.Repeat([]byte("a"), MaxUploadBytes+1), nil, ErrUploadTooLarge},
		{"too many rows", []byte(manyRows), nil, ErrTooManyRows},
		{"too many columns", []byte(wide), nil, ErrTooManyColumns},
		{"no title column", []byte("Risk ID,Category\nR-1,operational\n"), nil, ErrNoHeaderRow},
		{"no category column", []byte("Risk ID,Title\nR-1,Outage\n"), nil, ErrNoHeaderRow},
		{"unknown column map field", []byte("Title,Category\n"), map[string]string{"owner": "Owner"}, ErrInvalidColumnMap},
		{"one header mapped twice", []byte("Title,Category\n"), map[string]string{FieldTitle: "X", FieldDescription: "x"}, ErrInvalidColumnMap},
		{"mapped header absent", []byte("Title,Category\nA,operational\n"), map[string]string{FieldDescription: "Notes"}, ErrMappedColumnAbsent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCSVWith(tc.raw, ParseOptions{ColumnMap: tc.columnMap})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// The caps are inclusive: exactly MaxRows rows and MaxColumns columns parse.
	atCap := "Title,Category\n" + strings.Repeat("Risk,operational\n", MaxRows-1)
	if _, err := ParseCSVWith([]byte(atCap), ParseOptions{}); err != nil {
		t.Errorf("%d rows: %v", MaxRows, err)
	}
	atWidth := "Title,Category" + strings.Repeat(",x", MaxColumns-2) + "\nRisk,operational\n"
	if _, err := ParseCSVWith([]byte(atWidth), ParseOptions{}); err != nil {
		t.Errorf("%d columns: %v", MaxColumns, err)
	}
}

func TestParseXLSX(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	rows := [][]any{
		{"Risk ID", "Title", "Category", "Owner"},
		{"R-1", "Outage", "operational", "alice@corp.test"},
		{"r-1", "Repeat", "operational", ""},
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			t.Fatalf("SetSheetRow: %v", err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	got, err := Parse("register.xlsx", buf.Bytes(), ParseOptions{})
	if err != nil {
		t.Fatalf("Parse xlsx: %v", err)
	}
	want := []rowSummary{{Row: 2, Ref: "R-1", Title: "Outage", Cat: "operational", Unmapped: map[string]string{"Owner": "alice@corp.test"}}}
	if s := summarize(got.Risks); !reflect.DeepEqual(s, want) {
		t.Errorf("risks = %+v, want %+v", s, want)
	}
	if r := rejections(got.Rejected); !reflect.DeepEqual(r, map[int]string{3: RejectDuplicateExternal}) {
		t.Errorf("rejected = %v", r)
	}
	if _, err := Parse("register.txt", buf.Bytes(), ParseOptions{}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("txt: %v", err)
	}
}

func TestMatchCategory(t *testing.T) {
	t.Parallel()
	cats := []risk.Category{
		{Slug: "operational", Name: "Operational"},
		{Slug: "ops-old", Name: "Ops", Archived: true},
		{Slug: "a", Name: "Shared"},
		{Slug: "b", Name: "shared"},
	}
	for _, tc := range []struct {
		ref, want string
		ok        bool
	}{
		{"operational", "operational", true},
		{" Operational ", "operational", true},
		{"OPERATIONAL", "operational", true},
		{"ops-old", "", false},
		{"Ops", "", false},
		{"Shared", "", false},
		{"a", "a", true},
		{"nonsense", "", false},
	} {
		got, ok := MatchCategory(cats, tc.ref)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MatchCategory(%q) = %q, %v; want %q, %v", tc.ref, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFieldNames(t *testing.T) {
	t.Parallel()
	got := fmt.Sprint(FieldNames())
	if got != "[category description external_ref review_due title]" {
		t.Errorf("FieldNames = %s", got)
	}
}
