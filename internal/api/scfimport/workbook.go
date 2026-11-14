package scfimport

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/scfworkbook"
)

// Column headers on the published workbook's "SCF <release>" sheet. The
// description header carries a line break in the workbook; scfworkbook
// normalizes it away before comparing.
const (
	workbookDomainHeader      = "SCF Domain"
	workbookTitleHeader       = "SCF Control"
	workbookDescriptionHeader = "Secure Controls Framework (SCF) Control Description"
)

// WorkbookOptions overrides release metadata the workbook does not carry, or
// carries only in its sheet name.
type WorkbookOptions struct {
	ReleaseVersion string
	ReleaseDate    string
}

// LoadWorkbook reads the catalog from SCF's published Excel workbook
// (secure-controls-framework-scf-<release>.xlsx). It finds the control sheet
// the same way the crosswalk importer does (scfworkbook.ControlSheet) and
// takes the release version from that sheet's name unless opts overrides it.
//
// Family is the SCF # prefix ("IAC" for IAC-06), matching the JSON catalog
// and the rows already seeded from it; the "SCF Domain" column is used only
// when an ID has no prefix.
func LoadWorkbook(path string, opts WorkbookOptions) (*Catalog, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("scfimport: open SCF workbook %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sheet, err := scfworkbook.ControlSheet(f)
	if err != nil {
		return nil, fmt.Errorf("scfimport: SCF workbook %s: %w", path, err)
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("scfimport: read sheet %q: %w", sheet, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("scfimport: sheet %q has no data rows", sheet)
	}
	headers := rows[0]
	idCol := scfworkbook.FindExactHeader(headers, scfworkbook.AnchorHeader)
	titleCol := scfworkbook.FindExactHeader(headers, workbookTitleHeader)
	descCol := scfworkbook.FindExactHeader(headers, workbookDescriptionHeader)
	domainCol := scfworkbook.FindExactHeader(headers, workbookDomainHeader)
	for _, req := range []struct {
		col  int
		name string
	}{{idCol, scfworkbook.AnchorHeader}, {titleCol, workbookTitleHeader}, {descCol, workbookDescriptionHeader}} {
		if req.col < 0 {
			return nil, fmt.Errorf("scfimport: sheet %q missing required %q column", sheet, req.name)
		}
	}

	release := strings.TrimSpace(opts.ReleaseVersion)
	if release == "" {
		release = scfworkbook.SheetRelease(sheet)
	}
	if release == "" {
		return nil, fmt.Errorf("scfimport: sheet %q does not name a release; pass --release-version", sheet)
	}

	cat := &Catalog{
		SchemaVersion:  SchemaVersion,
		ReleaseVersion: release,
		ReleaseDate:    strings.TrimSpace(opts.ReleaseDate),
	}
	seen := map[string]int{}
	for i, row := range rows[1:] {
		id := scfworkbook.CellAt(row, idCol)
		if id == "" {
			continue
		}
		if first, dup := seen[id]; dup {
			return nil, fmt.Errorf("scfimport: sheet %q row %d repeats SCF # %s from row %d", sheet, i+2, id, first)
		}
		seen[id] = i + 2
		family := id
		if j := strings.Index(id, "-"); j > 0 {
			family = id[:j]
		} else if d := scfworkbook.CellAt(row, domainCol); d != "" {
			family = d
		}
		cat.Controls = append(cat.Controls, Control{
			SCFID:       id,
			Family:      family,
			Title:       scfworkbook.CellAt(row, titleCol),
			Description: scfworkbook.CellAt(row, descCol),
		})
	}
	if err := validate(cat); err != nil {
		return nil, fmt.Errorf("%w (sheet %q)", err, sheet)
	}
	return cat, nil
}
