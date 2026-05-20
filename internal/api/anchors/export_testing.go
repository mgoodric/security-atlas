// Slice 174 — test-only exports for the streaming-memory budget
// integration test (`export_integration_test.go`).
//
// The 50K-anchor streaming-memory test (AC-7) runs against synthetic
// in-process rows so it does NOT need to dirty the global SCF catalog
// with 50K seed rows. It needs to invoke the format-specific writers
// directly, but those writers are unexported package-internal helpers
// (correct posture — production callers go through ExportAnchors).
//
// This file re-exports the minimal surface the integration test needs
// under the `ExportTesting*` prefix so the symbols are obviously
// test-only at the import site. The file lives outside any build tag
// because Go does not support `//go:build integration_export_only`
// gating of exported symbols (the alternative — re-declaring the
// writers under the build tag — would duplicate logic).
//
// Slice 145 has a similar pattern in `internal/export/`. Slice 174
// mirrors it.

package anchors

import (
	"io"

	"github.com/google/uuid"
)

// ExportTestingAnchorRow is the test-only alias for anchorExportRow.
type ExportTestingAnchorRow struct {
	ID                 uuid.UUID
	SCFID              string
	Family             string
	Title              string
	Description        string
	FrameworkVersionID uuid.UUID
	FrameworkVersion   string
	FrameworkSlug      string
}

// ExportTestingEdgeRow is the test-only alias for edgeExportRow.
type ExportTestingEdgeRow struct {
	EdgeID                    uuid.UUID
	AnchorID                  uuid.UUID
	AnchorSCFID               string
	FrameworkRequirementID    uuid.UUID
	FrameworkRequirementCode  string
	FrameworkRequirementTitle string
	FrameworkSlug             string
	FrameworkVersion          string
	RelationshipType          string
	Strength                  float64
	SourceAttribution         string
	Rationale                 string
}

// ExportTestingWriteCSV invokes the slice 174 CSV writer against the
// supplied anchors + edges (grouped by anchor id). Test-only.
func ExportTestingWriteCSV(w io.Writer, anchors []ExportTestingAnchorRow, edgesByAnchor map[uuid.UUID][]ExportTestingEdgeRow) error {
	return writeAnchorsCSV(w, toAnchorRows(anchors), toEdgeMap(edgesByAnchor))
}

// ExportTestingWriteJSON invokes the slice 174 nested-JSON writer.
func ExportTestingWriteJSON(w io.Writer, anchors []ExportTestingAnchorRow, edgesByAnchor map[uuid.UUID][]ExportTestingEdgeRow) error {
	return writeAnchorsJSON(w, toAnchorRows(anchors), toEdgeMap(edgesByAnchor))
}

// ExportTestingWriteXLSX invokes the slice 174 two-sheet XLSX writer.
// edgesFlat is the flat ordered slice the XLSX Sheet 2 consumes.
func ExportTestingWriteXLSX(w io.Writer, anchors []ExportTestingAnchorRow, edgesFlat []ExportTestingEdgeRow) error {
	return writeAnchorsXLSX(w, toAnchorRows(anchors), toEdgeSlice(edgesFlat))
}

func toAnchorRows(in []ExportTestingAnchorRow) []anchorExportRow {
	out := make([]anchorExportRow, len(in))
	for i, a := range in {
		out[i] = anchorExportRow{
			ID:                 a.ID,
			SCFID:              a.SCFID,
			Family:             a.Family,
			Title:              a.Title,
			Description:        a.Description,
			FrameworkVersionID: a.FrameworkVersionID,
			FrameworkVersion:   a.FrameworkVersion,
			FrameworkSlug:      a.FrameworkSlug,
		}
	}
	return out
}

func toEdgeMap(in map[uuid.UUID][]ExportTestingEdgeRow) map[uuid.UUID][]edgeExportRow {
	out := make(map[uuid.UUID][]edgeExportRow, len(in))
	for k, v := range in {
		out[k] = toEdgeSlice(v)
	}
	return out
}

func toEdgeSlice(in []ExportTestingEdgeRow) []edgeExportRow {
	out := make([]edgeExportRow, len(in))
	for i, e := range in {
		// Two struct types share an identical field set; the explicit
		// conversion here is intentional — keeps the unit-suite alias
		// boundary obvious at the call site without an `if false`
		// guard that staticcheck would flag.
		out[i] = edgeExportRow(e)
	}
	return out
}
