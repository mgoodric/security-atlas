package soc2import

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/scfworkbook"
)

const (
	defaultWorkbookRelationship = "intersects_with"
	defaultWorkbookStrength     = 0.5
)

var errNotOLIRWorkbook = errors.New("not an SCF OLIR STRM workbook")

// SCFWorkbookOptions selects the framework columns and metadata when
// importing SCF's published Excel workbook. Empty metadata is derived from the
// workbook (from the first framework column) where possible.
//
// Each FrameworkColumns entry is a column header, optionally followed by
// "=PREFIX" to prepend PREFIX to every requirement code read from that column.
// Several entries combine into one framework: SCF maps ISO 27001's clauses
// under "ISO 27001 2022" but its Annex A controls under "ISO 27002 2022" as
// bare 5.1-8.34, so ISO 27001 takes both, the second as "ISO 27002 2022=A.".
type SCFWorkbookOptions struct {
	FrameworkColumns []string
	FrameworkSlug    string
	FrameworkName    string
	FrameworkIssuer  string
	FrameworkVersion string
	ReleaseDate      string
	// TitlesPath names a YAML file in the crosswalk requirements format
	// (code, title, body) whose titles take precedence over the bundled
	// crosswalk's. Its mappings, if any, are ignored.
	TitlesPath string
	// AllowCodeTitles lets requirements that no title source covers import
	// with their code as their title. Without it the load fails and lists them.
	AllowCodeTitles bool
}

type focalDocument struct {
	Header string
	FDI    string
	Issuer string
	Name   string
}

// LoadSCFWorkbook reads an SCF-format XLSX crosswalk. It supports both current
// SCF workbook shapes verified for this slice:
//
//   - the full SCF workbook: a wide "SCF <version>" sheet with "SCF #" and one
//     column per focal document, plus a "Focal Documents" metadata sheet.
//   - SCF OLIR STRM workbooks: a sheet with Focal Document Element,
//     Relationship, Reference Document Element, and Strength columns.
func LoadSCFWorkbook(path string, opts SCFWorkbookOptions) (*Crosswalk, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: open SCF workbook %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// The published SCF workbook carries sheets such as "STRM Errata" whose
	// header row can look like an OLIR sheet. When an "SCF <version>" sheet is
	// present the workbook is the wide shape, so OLIR detection is skipped.
	if !scfworkbook.HasVersionSheet(f) {
		if cw, err := loadOLIRWorkbook(f, path, opts); err == nil {
			return cw, nil
		} else if !errors.Is(err, errNotOLIRWorkbook) {
			return nil, err
		}
	}

	cw, err := loadWideSCFWorkbook(f, path, opts)
	if err != nil {
		return nil, err
	}
	return cw, nil
}

func loadWideSCFWorkbook(f *excelize.File, path string, opts SCFWorkbookOptions) (*Crosswalk, error) {
	sheet, err := scfworkbook.ControlSheet(f)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: SCF workbook %s: %w", path, err)
	}

	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: read sheet %q: %w", sheet, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("crosswalk: sheet %q has no data rows", sheet)
	}
	headers := rows[0]
	scfCol := findHeader(headers, "SCF #")
	if scfCol < 0 {
		return nil, fmt.Errorf("crosswalk: sheet %q missing required \"SCF #\" column", sheet)
	}

	focalDocs, focalProblem := loadFocalDocuments(f)
	columns, err := selectFrameworkColumns(headers, focalDocs, focalProblem, opts.FrameworkColumns)
	if err != nil {
		return nil, err
	}
	frameworkHeader := headers[columns[0].Index]
	meta, haveMeta := focalDocs[normalizeHeader(frameworkHeader)]
	// Without focal-document metadata the slug would fall back to the column
	// header, which can duplicate a framework already imported under its FDI
	// slug. Make the operator name it instead.
	if !haveMeta && opts.FrameworkSlug == "" {
		if focalProblem == "" {
			focalProblem = fmt.Sprintf("the Focal Documents sheet has no row for column %q", normalizeHeader(frameworkHeader))
		}
		return nil, fmt.Errorf("crosswalk: %s; pass --framework-slug", focalProblem)
	}

	reqs := map[string]Requirement{}
	seenEdges := map[string]struct{}{}
	mappings := make([]Mapping, 0)
	for _, col := range columns {
		header := headers[col.Index]
		colCodes, colMappings := 0, 0
		for rowIdx, row := range rows[1:] {
			scfAnchor := cellAt(row, scfCol)
			if scfAnchor == "" {
				continue
			}
			rawMappings := cellAt(row, col.Index)
			if rawMappings == "" {
				continue
			}
			for _, code := range splitWorkbookCodes(rawMappings) {
				if !strings.HasPrefix(code, col.Prefix) {
					code = col.Prefix + code
				}
				colCodes++
				reqs[code] = Requirement{Code: code, Title: code}
				key := code + "\x00" + scfAnchor
				if _, ok := seenEdges[key]; ok {
					continue
				}
				seenEdges[key] = struct{}{}
				colMappings++
				mappings = append(mappings, Mapping{
					RequirementCode:  code,
					SCFAnchor:        scfAnchor,
					RelationshipType: defaultWorkbookRelationship,
					Strength:         defaultWorkbookStrength,
					Rationale: fmt.Sprintf(
						"Imported from SCF workbook sheet %q row %d column %q; the full workbook lists ID mappings only, so this lands as community_draft pending STRM review.",
						sheet, rowIdx+2, header,
					),
				})
			}
		}
		if colCodes == 0 {
			return nil, fmt.Errorf("crosswalk: framework column %q in sheet %q has zero mappings", header, sheet)
		}
		if colMappings == 0 {
			return nil, fmt.Errorf("crosswalk: framework column %q in sheet %q adds no mappings beyond the columns before it; drop it or give it a distinct prefix", header, sheet)
		}
	}

	mappings, rollUp := rollUpPointsOfFocus(reqs, mappings)
	requirements := make([]Requirement, 0, len(reqs))
	for _, r := range reqs {
		requirements = append(requirements, r)
	}
	sort.Slice(requirements, func(i, j int) bool { return requirements[i].Code < requirements[j].Code })

	cw := &Crosswalk{
		SchemaVersion:     SchemaVersion,
		FrameworkSlug:     firstNonEmpty(opts.FrameworkSlug, slugFromFocal(meta, frameworkHeader)),
		FrameworkName:     firstNonEmpty(opts.FrameworkName, meta.Name, normalizeHeader(frameworkHeader)),
		FrameworkIssuer:   firstNonEmpty(opts.FrameworkIssuer, meta.Issuer),
		FrameworkVersion:  firstNonEmpty(opts.FrameworkVersion, versionFromFocal(meta, frameworkHeader), versionFromSCFSheet(sheet)),
		ReleaseDate:       opts.ReleaseDate,
		SourceAttribution: "community_draft",
		Requirements:      requirements,
		Mappings:          mappings,

		PointsOfFocusRollUp: rollUp,
	}
	if err := resolveWorkbookTitles(cw, opts); err != nil {
		return nil, err
	}
	if err := validate(cw); err != nil {
		return nil, err
	}
	return cw, nil
}

// focalDocumentsSheet finds the focal-document metadata sheet, published as
// "Focal Documents (FD)" in 2026.3 and as "Focal Documents" earlier.
func focalDocumentsSheet(f *excelize.File) string {
	for _, name := range f.GetSheetList() {
		norm := normalizeHeader(name)
		if norm == "Focal Documents" || strings.HasPrefix(norm, "Focal Documents (") {
			return name
		}
	}
	return ""
}

func loadOLIRWorkbook(f *excelize.File, path string, opts SCFWorkbookOptions) (*Crosswalk, error) {
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil || len(rows) < 2 {
			continue
		}
		headers := rows[0]
		fdeCol := findHeader(headers, "Focal Document Element")
		refCol := findHeader(headers, "Reference Document Element")
		relCol := findHeader(headers, "Relationship")
		strengthCol := findHeader(headers, "Strength of Relationship")
		if fdeCol < 0 || refCol < 0 || relCol < 0 || strengthCol < 0 {
			continue
		}

		reqs := map[string]Requirement{}
		seenEdges := map[string]struct{}{}
		mappings := make([]Mapping, 0)
		for rowIdx, row := range rows[1:] {
			reqCode := cellAt(row, fdeCol)
			scfAnchor := cellAt(row, refCol)
			if reqCode == "" || scfAnchor == "" || strings.EqualFold(reqCode, "N/A") || strings.EqualFold(scfAnchor, "N/A") {
				continue
			}
			rel, err := normalizeRelationship(cellAt(row, relCol))
			if err != nil {
				return nil, fmt.Errorf("crosswalk: sheet %q row %d: %w", sheet, rowIdx+2, err)
			}
			strength, err := normalizeStrength(cellAt(row, strengthCol))
			if err != nil {
				return nil, fmt.Errorf("crosswalk: sheet %q row %d: %w", sheet, rowIdx+2, err)
			}
			reqs[reqCode] = Requirement{Code: reqCode, Title: reqCode}
			key := reqCode + "\x00" + scfAnchor
			if _, ok := seenEdges[key]; ok {
				continue
			}
			seenEdges[key] = struct{}{}
			mappings = append(mappings, Mapping{
				RequirementCode:  reqCode,
				SCFAnchor:        scfAnchor,
				RelationshipType: rel,
				Strength:         strength,
				Rationale:        firstNonEmpty(cellAt(row, findHeader(headers, "Rationale")), "SCF OLIR STRM workbook"),
			})
		}
		if len(mappings) == 0 {
			return nil, fmt.Errorf("crosswalk: SCF OLIR sheet %q has zero importable mappings", sheet)
		}

		mappings, rollUp := rollUpPointsOfFocus(reqs, mappings)
		requirements := make([]Requirement, 0, len(reqs))
		for _, r := range reqs {
			requirements = append(requirements, r)
		}
		sort.Slice(requirements, func(i, j int) bool { return requirements[i].Code < requirements[j].Code })

		version := firstNonEmpty(opts.FrameworkVersion, generalInfoValue(f, "Focal Document Version"), generalInfoValue(f, "Reference Version"), versionFromFocal(focalDocument{Header: sheet, FDI: sheet}, sheet))
		cw := &Crosswalk{
			SchemaVersion:     SchemaVersion,
			FrameworkSlug:     firstNonEmpty(opts.FrameworkSlug, slugify(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))), slugify(sheet)),
			FrameworkName:     firstNonEmpty(opts.FrameworkName, sheet),
			FrameworkIssuer:   opts.FrameworkIssuer,
			FrameworkVersion:  version,
			ReleaseDate:       opts.ReleaseDate,
			SourceAttribution: "community_draft",
			Requirements:      requirements,
			Mappings:          mappings,

			PointsOfFocusRollUp: rollUp,
		}
		if err := resolveWorkbookTitles(cw, opts); err != nil {
			return nil, err
		}
		if err := validate(cw); err != nil {
			return nil, err
		}
		return cw, nil
	}
	return nil, fmt.Errorf("%w: %s", errNotOLIRWorkbook, path)
}

// loadFocalDocuments reads focal-document metadata keyed by normalized SCF
// column header. When it yields nothing, the second value says why.
func loadFocalDocuments(f *excelize.File) (map[string]focalDocument, string) {
	out := map[string]focalDocument{}
	sheet := focalDocumentsSheet(f)
	if sheet == "" {
		return out, `workbook has no "Focal Documents" sheet`
	}
	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) < 2 {
		return out, fmt.Sprintf("sheet %q has no data rows", sheet)
	}
	headers := rows[0]
	headerCol := findHeader(headers, "SCF Column Header")
	fdiCol := findHeader(headers, "Focal Document Identifier")
	sourceCol := findHeader(headers, "Source")
	nameCol := findHeader(headers, "Focal Document Name")
	if headerCol < 0 {
		return out, fmt.Sprintf(`sheet %q missing "SCF Column Header" column`, sheet)
	}
	for _, row := range rows[1:] {
		header := cellAt(row, headerCol)
		if header == "" {
			continue
		}
		out[normalizeHeader(header)] = focalDocument{
			Header: header,
			FDI:    cellAt(row, fdiCol),
			Issuer: cellAt(row, sourceCol),
			Name:   cellAt(row, nameCol),
		}
	}
	if len(out) == 0 {
		return out, fmt.Sprintf("sheet %q lists no SCF column headers", sheet)
	}
	return out, ""
}

// workbookColumn is one framework column picked for import, with the prefix
// prepended to each requirement code read from it.
type workbookColumn struct {
	Index  int
	Prefix string
}

func selectFrameworkColumns(headers []string, focalDocs map[string]focalDocument, focalProblem string, requested []string) ([]workbookColumn, error) {
	if len(requested) > 0 {
		out := make([]workbookColumn, 0, len(requested))
		for _, spec := range requested {
			col, err := resolveFrameworkColumn(headers, spec)
			if err != nil {
				return nil, err
			}
			out = append(out, col)
		}
		return out, nil
	}
	candidates := make([]int, 0)
	for i, h := range headers {
		if _, ok := focalDocs[normalizeHeader(h)]; ok {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 1 {
		return []workbookColumn{{Index: candidates[0]}}, nil
	}
	if len(candidates) == 0 {
		if focalProblem == "" {
			focalProblem = `no column header matches a Focal Documents "SCF Column Header"`
		}
		return nil, fmt.Errorf("crosswalk: no framework mapping columns found: %s; pass --framework-column and --framework-slug", focalProblem)
	}
	return nil, fmt.Errorf("crosswalk: workbook has %d framework columns; pass --framework-column", len(candidates))
}

// resolveFrameworkColumn reads a "HEADER" or "HEADER=PREFIX" spec. A spec that
// names a header outright wins, so a header containing "=" still resolves.
func resolveFrameworkColumn(headers []string, spec string) (workbookColumn, error) {
	if idx := findHeader(headers, spec); idx >= 0 {
		return workbookColumn{Index: idx}, nil
	}
	if i := strings.LastIndex(spec, "="); i >= 0 {
		header, prefix := spec[:i], strings.TrimSpace(spec[i+1:])
		if idx := findHeader(headers, header); idx >= 0 {
			return workbookColumn{Index: idx, Prefix: prefix}, nil
		}
		return workbookColumn{}, fmt.Errorf("crosswalk: framework column %q not found", header)
	}
	return workbookColumn{}, fmt.Errorf("crosswalk: framework column %q not found", spec)
}

// The header and cell helpers are shared with the catalog importer.
var (
	findHeader      = scfworkbook.FindHeader
	cellAt          = scfworkbook.CellAt
	normalizeHeader = scfworkbook.NormalizeHeader
)

func splitWorkbookCodes(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ';' || r == ','
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimPrefix(p, "•"))
		if p == "" || strings.EqualFold(p, "N/A") {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func normalizeRelationship(raw string) (string, error) {
	switch normalizeHeader(raw) {
	case "Equal To", "Equal":
		return "equal", nil
	case "Subset Of":
		return "subset_of", nil
	case "Superset Of":
		return "superset_of", nil
	case "Intersects With", "Intersects":
		return "intersects_with", nil
	case "No Relationship":
		return "no_relationship", nil
	default:
		return "", fmt.Errorf("relationship %q is not a supported STRM relationship", raw)
	}
}

func normalizeStrength(raw string) (float64, error) {
	if raw == "" {
		return 0, fmt.Errorf("strength is required")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("strength %q is not numeric", raw)
	}
	if n > 1 {
		n = n / 10
	}
	if n < 0 || n > 1 {
		return 0, fmt.Errorf("strength %q normalizes outside [0.0, 1.0]", raw)
	}
	return n, nil
}

func slugFromFocal(doc focalDocument, fallback string) string {
	if doc.FDI != "" {
		return slugify(doc.FDI)
	}
	return slugify(fallback)
}

func versionFromFocal(doc focalDocument, fallback string) string {
	for _, s := range []string{doc.FDI, doc.Header, fallback} {
		if v := lastVersionToken(s); v != "" {
			return v
		}
	}
	return ""
}

func versionFromSCFSheet(sheet string) string {
	return lastVersionToken(sheet)
}

func generalInfoValue(f *excelize.File, key string) string {
	rows, err := f.GetRows("General Information")
	if err != nil {
		return ""
	}
	want := normalizeHeader(key)
	for _, row := range rows {
		if len(row) >= 2 && normalizeHeader(row[0]) == want {
			return strings.TrimSpace(row[1])
		}
	}
	return ""
}

var versionTokenRe = regexp.MustCompile(`\d+(?:[.-]\d+)*`)

func lastVersionToken(s string) string {
	matches := versionTokenRe.FindAllString(strings.ReplaceAll(s, "_", "-"), -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.ReplaceAll(matches[len(matches)-1], "-", ".")
}

func slugify(s string) string {
	s = strings.ToLower(normalizeHeader(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
