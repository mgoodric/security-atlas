package mappingoverlays

// Bulk import and export of overlay edges (#1799):
//
//	POST /v1/mapping-overlays/import  a CSV or YAML file (?format=csv|yaml, or by Content-Type); ?partial=true
//	GET  /v1/mapping-overlays/export  the tenant's overlay edges as CSV (the list filters apply)

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
)

// maxImportBytes bounds the import body. MaxImportRows rows of short codes
// and a paragraph of rationale each fit well inside it.
const maxImportBytes = 4 << 20

// maxExportRows bounds the export (internal/export leaves the cap to the
// caller). A tenant's overlay edges are far fewer.
const maxExportRows = 100_000

type importedWire struct {
	Row         int      `json:"row"`
	OverlayEdge edgeWire `json:"overlay_edge"`
}

type rejectedWire struct {
	Row    int    `json:"row"`
	Line   int    `json:"line"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// Import handles POST /v1/mapping-overlays/import. 201 when edges were
// written; 422 with every bad row when nothing was (a bad row without
// ?partial=true, or no valid row at all); 400 for a file refused as a
// whole.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	if !requireOverlayWrite(w, r) || !requireTenant(w, r) {
		return
	}
	partial := false
	if p := r.URL.Query().Get("partial"); p != "" {
		v, err := strconv.ParseBool(p)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "partial must be true or false")
			return
		}
		partial = v
	}
	format, ok := importFormat(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnsupportedMediaType, "send the file as CSV or YAML: ?format=csv|yaml, or Content-Type text/csv or application/yaml")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "import body exceeds 4 MiB")
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "could not read body")
		return
	}
	parse := mappingoverlay.ParseCSV
	if format == "yaml" {
		parse = mappingoverlay.ParseYAML
	}
	rows, err := parse(bytes.NewReader(body))
	if err == nil {
		var res mappingoverlay.ImportResult
		res, err = h.store.Import(r.Context(), rows, partial, actorFrom(r))
		if err == nil {
			writeImportResult(w, res, partial)
			return
		}
	}
	var fileErr *mappingoverlay.FileError
	if errors.As(err, &fileErr) {
		httpresp.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": fileErr.Error(), "code": "invalid_import_file"})
		return
	}
	httperr.WriteInternal(w, r, "import overlay edges", err)
}

func writeImportResult(w http.ResponseWriter, res mappingoverlay.ImportResult, partial bool) {
	created := make([]importedWire, len(res.Created))
	for i, c := range res.Created {
		created[i] = importedWire{Row: c.Row, OverlayEdge: wireFrom(c.Edge)}
	}
	rejected := make([]rejectedWire, len(res.Rejected))
	for i, rj := range res.Rejected {
		rejected[i] = rejectedWire{Row: rj.Row, Line: rj.Line, Code: rj.Code, Reason: rj.Reason}
	}
	status := http.StatusCreated
	if !res.Written {
		status = http.StatusUnprocessableEntity
	}
	httpresp.WriteJSON(w, status, map[string]any{
		"written":  res.Written,
		"partial":  partial,
		"created":  created,
		"rejected": rejected,
	})
}

// importFormat picks csv or yaml from ?format=, else from the Content-Type.
func importFormat(r *http.Request) (string, bool) {
	if f := r.URL.Query().Get("format"); f != "" {
		return f, f == "csv" || f == "yaml"
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", false
	}
	switch mt {
	case "text/csv":
		return "csv", true
	case "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml":
		return "yaml", true
	}
	return "", false
}

// Export handles GET /v1/mapping-overlays/export: the tenant's overlay
// edges, every status unless ?status= narrows it, as CSV. Rationale and
// source are organisation-entered text, so the cells go through the shared
// encoder's formula neutralisation.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if !requireTenant(w, r) {
		return
	}
	q := r.URL.Query()
	edges, err := h.store.List(r.Context(), mappingoverlay.Filter{
		Framework:   q.Get("framework"),
		Requirement: q.Get("requirement"),
		Anchor:      q.Get("anchor"),
		Status:      q.Get("status"),
	})
	if err != nil {
		writeErr(w, r, "export overlay edges", err)
		return
	}
	if len(edges) > maxExportRows {
		httpresp.WriteError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("export exceeds %d rows; narrow it with framework, requirement, anchor or status", maxExportRows))
		return
	}
	enc := export.NewCSVExporter()
	var buf bytes.Buffer
	rows := func(yield func([]string) bool) {
		for _, e := range edges {
			if !yield(mappingoverlay.ExportRow(e)) {
				return
			}
		}
	}
	if err := enc.WriteRows(&buf, slices.Clone(mappingoverlay.ExportColumns), rows); err != nil {
		httperr.WriteInternal(w, r, "encode overlay export", err)
		return
	}
	w.Header().Set("Content-Type", enc.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+export.BuildFilename("mapping-overlays", enc.FileExt(), nil)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
