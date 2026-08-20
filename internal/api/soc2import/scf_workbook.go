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
)

const (
	defaultWorkbookRelationship = "intersects_with"
	defaultWorkbookStrength     = 0.5
)

var errNotOLIRWorkbook = errors.New("not an SCF OLIR STRM workbook")

// SCFWorkbookOptions selects the framework column and metadata when importing
// SCF's published Excel workbook. Empty metadata is derived from the workbook
// where possible.
type SCFWorkbookOptions struct {
	FrameworkColumn  string
	FrameworkSlug    string
	FrameworkName    string
	FrameworkIssuer  string
	FrameworkVersion string
	ReleaseDate      string
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

	if cw, err := loadOLIRWorkbook(f, path, opts); err == nil {
		return cw, nil
	} else if !errors.Is(err, errNotOLIRWorkbook) {
		return nil, err
	}

	cw, err := loadWideSCFWorkbook(f, path, opts)
	if err != nil {
		return nil, err
	}
	return cw, nil
}

func loadWideSCFWorkbook(f *excelize.File, path string, opts SCFWorkbookOptions) (*Crosswalk, error) {
	sheet := ""
	for _, name := range f.GetSheetList() {
		norm := normalizeHeader(name)
		if strings.HasPrefix(norm, "SCF ") {
			sheet = name
			break
		}
	}
	if sheet == "" {
		return nil, fmt.Errorf("crosswalk: SCF workbook %s missing an \"SCF <version>\" sheet", path)
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

	focalDocs := loadFocalDocuments(f)
	frameworkCol, frameworkHeader, err := selectFrameworkColumn(headers, focalDocs, opts.FrameworkColumn)
	if err != nil {
		return nil, err
	}
	meta := focalDocs[normalizeHeader(frameworkHeader)]

	reqs := map[string]Requirement{}
	seenEdges := map[string]struct{}{}
	mappings := make([]Mapping, 0)
	for rowIdx, row := range rows[1:] {
		scfAnchor := cellAt(row, scfCol)
		if scfAnchor == "" {
			continue
		}
		rawMappings := cellAt(row, frameworkCol)
		if rawMappings == "" {
			continue
		}
		for _, code := range splitWorkbookCodes(rawMappings) {
			reqs[code] = Requirement{Code: code, Title: code}
			key := code + "\x00" + scfAnchor
			if _, ok := seenEdges[key]; ok {
				continue
			}
			seenEdges[key] = struct{}{}
			mappings = append(mappings, Mapping{
				RequirementCode:  code,
				SCFAnchor:        scfAnchor,
				RelationshipType: defaultWorkbookRelationship,
				Strength:         defaultWorkbookStrength,
				Rationale: fmt.Sprintf(
					"Imported from SCF workbook sheet %q row %d column %q; the full workbook lists ID mappings only, so this lands as community_draft pending STRM review.",
					sheet, rowIdx+2, frameworkHeader,
				),
			})
		}
	}

	if len(mappings) == 0 {
		return nil, fmt.Errorf("crosswalk: framework column %q in sheet %q has zero mappings", frameworkHeader, sheet)
	}

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
	}
	if err := validate(cw); err != nil {
		return nil, err
	}
	return cw, nil
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
		}
		if err := validate(cw); err != nil {
			return nil, err
		}
		return cw, nil
	}
	return nil, fmt.Errorf("%w: %s", errNotOLIRWorkbook, path)
}

func loadFocalDocuments(f *excelize.File) map[string]focalDocument {
	out := map[string]focalDocument{}
	rows, err := f.GetRows("Focal Documents")
	if err != nil || len(rows) < 2 {
		return out
	}
	headers := rows[0]
	headerCol := findHeader(headers, "SCF Column Header")
	fdiCol := findHeader(headers, "Focal Document Identifier")
	sourceCol := findHeader(headers, "Source")
	nameCol := findHeader(headers, "Focal Document Name")
	if headerCol < 0 {
		return out
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
	return out
}

func selectFrameworkColumn(headers []string, focalDocs map[string]focalDocument, requested string) (int, string, error) {
	if requested != "" {
		idx := findHeader(headers, requested)
		if idx < 0 {
			return -1, "", fmt.Errorf("crosswalk: framework column %q not found", requested)
		}
		return idx, headers[idx], nil
	}
	candidates := make([]int, 0)
	for i, h := range headers {
		if _, ok := focalDocs[normalizeHeader(h)]; ok {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 1 {
		idx := candidates[0]
		return idx, headers[idx], nil
	}
	if len(candidates) == 0 {
		return -1, "", fmt.Errorf("crosswalk: no framework mapping columns found; pass --framework-column")
	}
	return -1, "", fmt.Errorf("crosswalk: workbook has %d framework columns; pass --framework-column", len(candidates))
}

func findHeader(headers []string, want string) int {
	nwant := normalizeHeader(want)
	for i, h := range headers {
		nh := normalizeHeader(h)
		if nh == nwant || strings.HasPrefix(nh, nwant+" ") {
			return i
		}
	}
	return -1
}

func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

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

func normalizeHeader(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u00a0", " ")), " ")
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
