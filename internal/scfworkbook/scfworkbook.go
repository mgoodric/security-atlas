// Package scfworkbook holds the sheet and header detection shared by the two
// importers that read the Secure Controls Framework's published Excel
// workbook: the catalog importer (internal/api/scfimport) and the crosswalk
// importer (internal/api/soc2import). Both must land on the same control
// sheet, so the rule for finding it lives here once.
package scfworkbook

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// AnchorHeader is the control-ID column on the control sheet.
const AnchorHeader = "SCF #"

// versionSheetRe matches the control sheet's name in the published SCF
// workbook ("SCF 2026.3"). Other "SCF ..." sheets, such as "SCF Domains &
// Principles", sort ahead of it and must not be taken for it.
var versionSheetRe = regexp.MustCompile(`^SCF (\d{4}(?:\.\d+)+)$`)

// HasVersionSheet reports whether the workbook carries an "SCF <yyyy>.<n>"
// control sheet.
func HasVersionSheet(f *excelize.File) bool {
	for _, name := range f.GetSheetList() {
		if versionSheetRe.MatchString(NormalizeHeader(name)) {
			return true
		}
	}
	return false
}

// ControlSheet picks the control sheet by name, never by position: the first
// "SCF <yyyy>.<n>" sheet, else the first "SCF ..." sheet whose header row
// carries an "SCF #" column.
func ControlSheet(f *excelize.File) (string, error) {
	var fallback []string
	for _, name := range f.GetSheetList() {
		norm := NormalizeHeader(name)
		if versionSheetRe.MatchString(norm) {
			return name, nil
		}
		if strings.HasPrefix(norm, "SCF ") {
			fallback = append(fallback, name)
		}
	}
	for _, name := range fallback {
		rows, err := f.GetRows(name)
		if err == nil && len(rows) > 0 && FindHeader(rows[0], AnchorHeader) >= 0 {
			return name, nil
		}
	}
	if len(fallback) > 0 {
		quoted := make([]string, len(fallback))
		for i, name := range fallback {
			quoted[i] = strconv.Quote(name)
		}
		return "", fmt.Errorf("missing an \"SCF <version>\" sheet with an \"SCF #\" column (found %s)", strings.Join(quoted, ", "))
	}
	return "", errors.New("missing an \"SCF <version>\" sheet")
}

// SheetRelease returns the release named by an "SCF <yyyy>.<n>" sheet
// ("2026.3"), or "" when the sheet is not named that way.
func SheetRelease(sheet string) string {
	m := versionSheetRe.FindStringSubmatch(NormalizeHeader(sheet))
	if m == nil {
		return ""
	}
	return m[1]
}

// NormalizeHeader collapses the line breaks, non-breaking spaces and runs of
// whitespace the workbook puts in its header cells, so
// "Secure Controls Framework (SCF)\nControl Description" compares equal to
// "Secure Controls Framework (SCF) Control Description".
func NormalizeHeader(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

// FindHeader returns the index of the column whose normalized header equals
// want or starts with want followed by a space, or -1.
func FindHeader(headers []string, want string) int {
	nwant := NormalizeHeader(want)
	for i, h := range headers {
		nh := NormalizeHeader(h)
		if nh == nwant || strings.HasPrefix(nh, nwant+" ") {
			return i
		}
	}
	return -1
}

// FindExactHeader returns the index of the column whose normalized header
// equals want, or -1. Use it where a prefix match would hit a sibling column.
func FindExactHeader(headers []string, want string) int {
	nwant := NormalizeHeader(want)
	for i, h := range headers {
		if NormalizeHeader(h) == nwant {
			return i
		}
	}
	return -1
}

// CellAt returns the trimmed cell at idx, or "" when the row is short or idx
// is negative.
func CellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}
