package riskimport

import (
	"archive/zip"
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
			name: "a notes row holding alias words is not taken for the header",
			csv:  "Risk,Reference,\n,,\nTitle,Category,Ref\nOutage,operational,R-1\n",
			wantRisks: []rowSummary{{Row: 4, Ref: "R-1", Title: "Outage", Cat: "operational",
				Unmapped: map[string]string{}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{},
		},
		{
			name:      "a column-mapped header is found below a title row",
			csv:       "Q3 register,,\nHeadline,Bucket,Notes\nOutage,operational,kept\n",
			columnMap: map[string]string{FieldTitle: "Headline", FieldCategory: "Bucket"},
			wantRisks: []rowSummary{{Row: 3, Title: "Outage", Cat: "operational",
				Unmapped: map[string]string{"Notes": "kept"}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{"Notes"},
		},
		{
			name: "the risk export's own headers re-import without a map",
			csv: "id,title,description,category,treatment,review_due_at,category_name,external_ref\n" +
				"6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839,Outage,Vendor down,operational,untreated,2027-02-01T00:00:00Z,Operational,R-1\n",
			wantRisks: []rowSummary{{Row: 2, Ref: "R-1", Title: "Outage", Desc: "Vendor down", Cat: "operational", Due: "2027-02-01",
				Unmapped: map[string]string{"id": "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839", "category_name": "Operational"}}},
			wantRejected: map[int]string{},
			wantUnmapped: []string{"id", "category_name"},
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
			csv:  "Title,Category,Description,Notes,,Target Date\nOutage,operational,Vendor down,see ticket,stray,2027-03-01\nSecond,operational,,,,\n",
			wantRisks: []rowSummary{
				{Row: 2, Title: "Outage", Desc: "Vendor down", Cat: "operational",
					Unmapped: map[string]string{"Notes": "see ticket", "column 5": "stray", "Target Date": "2027-03-01"}},
				{Row: 3, Title: "Second", Cat: "operational", Unmapped: map[string]string{}},
			},
			wantRejected: map[int]string{},
			wantUnmapped: []string{"Notes", "column 5", "Target Date"},
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
		{"header below the search window", []byte(strings.Repeat("note\n", headerSearchRows) + "Title,Category\nA,operational\n"), nil, ErrNoHeaderRow},
		{"unknown column map field", []byte("Title,Category\n"), map[string]string{"nonsense": "Owner"}, ErrInvalidColumnMap},
		{"one header mapped twice", []byte("Title,Category\n"), map[string]string{FieldTitle: "X", FieldDescription: "x"}, ErrInvalidColumnMap},
		{"mapped header absent", []byte("Title,Category\nA,operational\n"), map[string]string{FieldDescription: "Notes"}, ErrMappedColumnAbsent},
		{"required field mapped to an absent header", []byte("Title,Category\nA,operational\n"), map[string]string{FieldTitle: "Headline"}, ErrMappedColumnAbsent},
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

// xlsxOf writes rows to the first sheet of a new workbook, each row at
// the sheet row its index names (row 0 is sheet row 1); a nil row is left
// empty.
func xlsxOf(t *testing.T, rows [][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for i, r := range rows {
		if r == nil {
			continue
		}
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			t.Fatalf("SetSheetRow: %v", err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestParseXLSX(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		rows         [][]any
		wantRisks    []rowSummary
		wantRejected map[int]string
	}{
		{
			name: "header on the first row",
			rows: [][]any{
				{"Risk ID", "Title", "Category", "Notes"},
				{"R-1", "Outage", "operational", "alice@corp.test"},
				{"r-1", "Repeat", "operational", ""},
			},
			wantRisks:    []rowSummary{{Row: 2, Ref: "R-1", Title: "Outage", Cat: "operational", Unmapped: map[string]string{"Notes": "alice@corp.test"}}},
			wantRejected: map[int]string{3: RejectDuplicateExternal},
		},
		{
			// Row numbers are the sheet's own, so a rejected row can be
			// found in the workbook the user uploaded.
			name: "a title, a blank row and notes above the header",
			rows: [][]any{
				{"Acme risk register"},
				nil,
				{"Exported for the Q3 review", "Risk"},
				{"Risk ID", "Title", "Category", "Review Due"},
				{"R-1", "Outage", "operational", "2027-02-01"},
				nil,
				{"R-2", "Bad date", "operational", "soon"},
			},
			wantRisks:    []rowSummary{{Row: 5, Ref: "R-1", Title: "Outage", Cat: "operational", Due: "2027-02-01", Unmapped: map[string]string{}}},
			wantRejected: map[int]string{7: RejectInvalidReviewDue},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse("register.xlsx", xlsxOf(t, tc.rows), ParseOptions{})
			if err != nil {
				t.Fatalf("Parse xlsx: %v", err)
			}
			if s := summarize(got.Risks); !reflect.DeepEqual(s, tc.wantRisks) {
				t.Errorf("risks =\n%+v\nwant\n%+v", s, tc.wantRisks)
			}
			if r := rejections(got.Rejected); !reflect.DeepEqual(r, tc.wantRejected) {
				t.Errorf("rejected = %v, want %v", r, tc.wantRejected)
			}
		})
	}
	if _, err := Parse("register.txt", xlsxOf(t, cases[0].rows), ParseOptions{}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("txt: %v", err)
	}
}

// TestParseXLSXBounds: an XLSX upload is held to the CSV caps, through the
// bounded reader, and the caps are inclusive.
func TestParseXLSXBounds(t *testing.T) {
	t.Parallel()
	rowsOf := func(n int) [][]any {
		rows := [][]any{{"Title", "Category"}}
		for len(rows) < n {
			rows = append(rows, []any{"Risk", "operational"})
		}
		return rows
	}
	wideOf := func(n int) [][]any {
		header := []any{"Title", "Category"}
		for len(header) < n {
			header = append(header, fmt.Sprintf("Extra %d", len(header)))
		}
		return [][]any{header, {"Risk", "operational"}}
	}
	// A sheet whose inline XML inflates past MaxUnzipBytes while the
	// upload itself stays under MaxUploadBytes.
	bomb := func() []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("xl/worksheets/sheet1.xml")
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		chunk := bytes.Repeat([]byte{' '}, 1<<20)
		for i := 0; i <= MaxUnzipBytes>>20; i++ {
			if _, err := w.Write(chunk); err != nil {
				t.Fatalf("zip write: %v", err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("zip close: %v", err)
		}
		if buf.Len() > MaxUploadBytes {
			t.Fatalf("bomb is %d bytes, over the upload cap", buf.Len())
		}
		return buf.Bytes()
	}

	for _, tc := range []struct {
		name string
		raw  func() []byte
		want error
	}{
		{"empty", func() []byte { return nil }, ErrEmptyFile},
		{"over the upload cap", func() []byte { return bytes.Repeat([]byte("a"), MaxUploadBytes+1) }, ErrUploadTooLarge},
		{"inflates past the uncompressed cap", bomb, ErrWorkbookTooLarge},
		{"one row over the row cap", func() []byte { return xlsxOf(t, rowsOf(MaxRows+1)) }, ErrTooManyRows},
		{"one column over the column cap", func() []byte { return xlsxOf(t, wideOf(MaxColumns+1)) }, ErrTooManyColumns},
		{"no header row", func() []byte { return xlsxOf(t, [][]any{{"Risk ID", "Owner"}, {"R-1", "a"}}) }, ErrNoHeaderRow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse("register.xlsx", tc.raw(), ParseOptions{}); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	got, err := Parse("register.xlsx", xlsxOf(t, rowsOf(MaxRows)), ParseOptions{})
	if err != nil || len(got.Risks) != MaxRows-1 {
		t.Errorf("%d rows: err=%v staged=%d", MaxRows, err, len(got.Risks))
	}
	if _, err := Parse("register.xlsx", xlsxOf(t, wideOf(MaxColumns)), ParseOptions{}); err != nil {
		t.Errorf("%d columns: %v", MaxColumns, err)
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
	want := "[accepted_until accepter category description external_ref inherent_impact inherent_likelihood " +
		"inherent_loss_event_frequency inherent_loss_magnitude instrument_reference justification linked_controls owner " +
		"residual_impact residual_likelihood residual_loss_event_frequency residual_loss_magnitude review_due title treatment]"
	if got != want {
		t.Errorf("FieldNames = %s", got)
	}
}
