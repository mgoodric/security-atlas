package scfworkbook

import (
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func newBook(t *testing.T, sheets []string, headers map[string][]interface{}) *excelize.File {
	t.Helper()
	f := excelize.NewFile()
	t.Cleanup(func() { _ = f.Close() })
	for i, name := range sheets {
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				t.Fatal(err)
			}
		} else if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		if h, ok := headers[name]; ok {
			if err := f.SetSheetRow(name, "A1", &h); err != nil {
				t.Fatal(err)
			}
		}
	}
	return f
}

func TestControlSheet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		sheets  []string
		headers map[string][]interface{}
		want    string
		wantErr string
	}{
		{"version sheet after domains", []string{"SCF Domains & Principles", "SCF 2026.3"}, nil, "SCF 2026.3", ""},
		{"patch release", []string{"SCF Domains & Principles", "SCF 2026.3.1"}, nil, "SCF 2026.3.1", ""},
		{"fallback by SCF # header", []string{"SCF Domains & Principles", "SCF Controls"}, map[string][]interface{}{"SCF Controls": {"SCF Domain", "SCF #"}}, "SCF Controls", ""},
		{"SCF sheets without SCF #", []string{"SCF Domains & Principles"}, nil, "", `found "SCF Domains & Principles"`},
		{"no SCF sheet", []string{"Other"}, nil, "", `missing an "SCF <version>" sheet`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newBook(t, tc.sheets, tc.headers)
			got, err := ControlSheet(f)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v; want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ControlSheet = %q, %v; want %q", got, err, tc.want)
			}
			if HasVersionSheet(f) != (SheetRelease(tc.want) != "") {
				t.Fatalf("HasVersionSheet disagrees with SheetRelease for %q", tc.want)
			}
		})
	}
}

func TestSheetRelease(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"SCF 2026.3": "2026.3", "SCF  2026.3.1": "2026.3.1", "SCF Controls": "", "SCF 2026": ""} {
		if got := SheetRelease(in); got != want {
			t.Errorf("SheetRelease(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestHeaders(t *testing.T) {
	t.Parallel()
	headers := []string{"SCF Domain", "SCF Control", "SCF #", "Secure Controls Framework (SCF)\nControl Description", "SCF Control Question"}
	if got := FindHeader(headers, "Secure Controls Framework (SCF) Control Description"); got != 3 {
		t.Errorf("FindHeader description = %d", got)
	}
	if got := FindHeader(headers, "Secure Controls Framework (SCF)"); got != 3 {
		t.Errorf("FindHeader prefix = %d", got)
	}
	if got := FindExactHeader(headers, "SCF Control"); got != 1 {
		t.Errorf("FindExactHeader = %d", got)
	}
	if got := FindExactHeader(headers, "SCF Control Question"); got != 4 {
		t.Errorf("FindExactHeader nbsp = %d", got)
	}
	if FindHeader(headers, "Missing") != -1 || FindExactHeader(headers, "SCF") != -1 {
		t.Error("absent header should return -1")
	}
	row := []string{" a ", "b"}
	if CellAt(row, 0) != "a" || CellAt(row, 2) != "" || CellAt(row, -1) != "" {
		t.Error("CellAt bounds")
	}
}
