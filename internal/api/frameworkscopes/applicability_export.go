package frameworkscopes

// Statement of Applicability export (issue #1791, spec #1744).
// Route:
//
//	GET /v1/framework-scopes/applicability/export?framework_version=…&format=csv|xlsx[&revision=…|&as_of=…]
//
// One row per requirement of the framework version, from the activated
// revision by default, the named revision with ?revision=, or the revision in
// force at ?as_of= (RFC3339). With no revision selected, every requirement is
// exported undecided. Contradiction flags are computed against current control
// evaluations, as on the decision list (#1789 D6).
//
// CSV is the bare table, header first, so it reads back through the decision
// import (#1790). The xlsx carries the title ("Statement of Applicability" for
// ISO/IEC 27001, "Applicability" otherwise), the framework and version, the
// revision and the counts above the same table.

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

const (
	contentTypeCSV  = "text/csv; charset=utf-8"
	contentTypeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

// ExportApplicability — GET /v1/framework-scopes/applicability/export.
func (h *Handler) ExportApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	q := r.URL.Query()
	fvID, err := uuid.Parse(strings.TrimSpace(q.Get("framework_version")))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version must be a UUID")
		return
	}
	format := strings.TrimSpace(q.Get("format"))
	if format != "csv" && format != "xlsx" {
		httpresp.WriteError(w, http.StatusBadRequest, "format must be csv or xlsx")
		return
	}
	sel, ok := parseSelector(w, r)
	if !ok {
		return
	}

	exp, err := h.store.SoAExport(ctx, fvID, sel)
	if err != nil {
		switch {
		case errors.Is(err, frameworkscope.ErrFrameworkVersionNotFound):
			httpresp.WriteError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, frameworkscope.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
		case errors.Is(err, frameworkscope.ErrRevisionMismatch):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			httperr.WriteInternal(w, r, "export applicability", err)
		}
		return
	}

	var buf bytes.Buffer
	contentType := contentTypeCSV
	if format == "csv" {
		err = writeSoACSV(&buf, exp)
	} else {
		contentType = contentTypeXLSX
		err = writeSoAXLSX(&buf, exp)
	}
	if err != nil {
		httperr.WriteInternal(w, r, "encode applicability export", err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+soaFilename(exp, format)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// parseSelector reads ?revision= (a scope id) or ?as_of= (RFC3339); at most
// one. It writes a 400 and returns false on a bad value.
func parseSelector(w http.ResponseWriter, r *http.Request) (frameworkscope.SoASelector, bool) {
	var sel frameworkscope.SoASelector
	q := r.URL.Query()
	rev, asOf := strings.TrimSpace(q.Get("revision")), strings.TrimSpace(q.Get("as_of"))
	if rev != "" && asOf != "" {
		httpresp.WriteError(w, http.StatusBadRequest, "revision and as_of are mutually exclusive")
		return sel, false
	}
	if rev != "" {
		id, err := uuid.Parse(rev)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "revision must be a UUID")
			return sel, false
		}
		sel.Revision = &id
	}
	if asOf != "" {
		t, err := time.Parse(time.RFC3339, asOf)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "as_of must be RFC3339: "+err.Error())
			return sel, false
		}
		sel.AsOf = &t
	}
	return sel, true
}

var filenameUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// soaFilename is e.g. statement-of-applicability-iso27001-2022.xlsx.
func soaFilename(e frameworkscope.SoAExport, format string) string {
	slug := func(s string) string {
		return strings.Trim(filenameUnsafe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	}
	return fmt.Sprintf("%s-%s-%s.%s", slug(e.Title()), slug(e.FrameworkSlug), slug(e.FrameworkVersion), format)
}

// writeSoACSV writes the header and one row per requirement, every cell
// neutralised against spreadsheet formula injection.
func writeSoACSV(buf *bytes.Buffer, e frameworkscope.SoAExport) error {
	cw := csv.NewWriter(buf)
	if err := cw.Write(frameworkscope.SoAHeader); err != nil {
		return err
	}
	for _, row := range e.Rows() {
		for i, cell := range row {
			row[i] = neutraliseFormula(cell)
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// neutraliseFormula prefixes a cell that a spreadsheet would read as a
// formula with a single quote (OWASP CSV injection), the same rule as
// internal/export.
func neutraliseFormula(cell string) string {
	if cell == "" {
		return cell
	}
	switch cell[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + cell
	}
	return cell
}

// writeSoAXLSX writes one sheet named for the title: the title, the
// framework and version, the revision and the counts, then the table. Every cell is written as a string, never a formula, so a
// justification beginning with `=` stays text.
func writeSoAXLSX(buf *bytes.Buffer, e frameworkscope.SoAExport) (err error) {
	f := excelize.NewFile()
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	sheet := e.Title()
	if err = f.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}
	if err = f.SetDocProps(&excelize.DocProperties{Title: e.Title() + " - " + e.Subtitle()}); err != nil {
		return err
	}
	revision := "Scope revision: none in force"
	if sc := e.Applicability.Scope; sc != nil {
		revision = fmt.Sprintf("Scope revision: %s (%s, %s)", sc.Name, sc.ID, sc.State)
	}
	c := e.Applicability.Counts
	rows := [][]string{
		{e.Title()},
		{e.Subtitle()},
		{revision},
		{fmt.Sprintf("Requirements: %d · applicable: %d · excluded: %d · undecided: %d · contradictions: %d",
			c.Total, c.Applicable, c.Excluded, c.Undecided, c.Contradictions)},
		frameworkscope.SoAHeader,
	}
	rows = append(rows, e.Rows()...)
	for i, row := range rows {
		for j, cell := range row {
			ref, cerr := excelize.CoordinatesToCellName(j+1, i+1)
			if cerr != nil {
				return cerr
			}
			if err = f.SetCellStr(sheet, ref, cell); err != nil {
				return err
			}
		}
	}
	return f.Write(buf)
}
