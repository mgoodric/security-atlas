// Slice 174 — UCF anchor catalog export handler.
//
// `GET /v1/anchors/export?format=<csv|json|xlsx>` exports the SCF
// anchor catalog (anchor metadata + framework satisfactions inline)
// in three native projections per the maintainer-locked D1:
//
//   - csv  → flat-nested fallback. One row per anchor; framework
//            satisfactions JSON-stringified into a column.
//   - json → nested. One object per anchor; framework satisfactions
//            in a `framework_satisfactions` array field.
//   - xlsx → two-sheet workbook. Sheet 1 = Anchors; Sheet 2 = Edges
//            (one row per anchor → requirement edge).
//
// Reuses slice 135's `internal/export/` library for the CSV cell-
// injection sanitizer + the filename builder; the JSON and XLSX
// projections are slice-specific shapes built locally in this
// package (the generic library exposes single-sheet flat encoders
// only — see slice 174 D6 in the decisions log).
//
// Constitutional posture:
//
//   - Invariant #7 (SCF is the canonical control catalog): the
//     export is the catalog dump itself. Public-domain data; no
//     tenant_id filter on the catalog tables (no RLS).
//
//   - Slice 174 P0-A-174-1: tenant-private columns excluded. No
//     `applicability_expr`; no `controls.*` data. Only public SCF
//     catalog + STRM crosswalk metadata.
//
//   - Slice 174 P0-A-174-2: two-sheet XLSX writer is structurally
//     incapable of emitting charts, named ranges, or VBA — the code
//     paths for those zip members do not exist. Test pins the exact
//     six-zip-member list.
//
//   - Slice 135 P0-A4 (meta-audit on every outcome). The
//     `me_audit_log` write is tenant-scoped (under the caller's
//     tenant GUC) and uses action='anchors_export'.
//
//   - Slice 145 concurrency cap: every successful acquire defers a
//     release; refusal returns 429 with Retry-After: 30.
//
//   - Streaming write: each format's writer consumes the row
//     iterators pull-style. Per-row allocation is bounded; the
//     `TestSlice174_StreamingMemoryUnder200MB` integration test
//     asserts heap delta stays under 200 MB at synthetic-50K-anchor
//     scale.
//
// Role gate (slice 174 D4): NONE in the handler. The endpoint is
// admitted for any authenticated user — same admit set as the
// `/v1/anchors` read endpoint. The upstream OPA middleware enforces
// the catalog-resources allow rule. Slice 174 P0-A9 admit-set
// parity test pins it at the rego layer (see
// `internal/authz/slice174_test.go`).

package anchors

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
)

const (
	// defaultAnchorsExportRowCap is the slice 174 D3 row-cap default.
	// The SCF catalog runs ~1,400 anchors at current release; 50K
	// leaves 35x headroom for future SCF growth without risking an
	// unbounded export. Distinct from slice 137's 500K cap because the
	// anchors catalog is bounded — tenant control sets are unbounded.
	defaultAnchorsExportRowCap = 50_000

	// metaAuditActionAnchorsExport is the slice 174 D2 meta-audit
	// action value. Plural matches the slice 137 / 138 / 139
	// convention; the migration
	// `20260520010000_anchors_export_meta_audit.sql` extends the
	// `me_audit_log.action` CHECK to permit this value.
	metaAuditActionAnchorsExport = "anchors_export"

	// anchorsExportEntity is the slice 135 BuildFilename entity
	// identifier. Downloaded filenames look like
	// `anchors_20260520.csv` / `.json` / `.xlsx`.
	anchorsExportEntity = "anchors"
)

// ExportHandler owns the slice 174 anchor catalog export endpoint.
// The pool is needed for the meta-audit write (which uses its own
// short transaction outside the export's iteration window so the
// meta-audit row commits even when the streaming write is in flight).
//
// Slice 145 hook: the optional `limiter` field overrides the
// process-wide singleton [export.DefaultLimiter] when set — used by
// integration tests to pin a small, deterministic concurrency cap.
type ExportHandler struct {
	source  anchorsExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

// anchorsExportSource is the minimal interface the handler needs.
// Keeps the test surface small and avoids standing up a real pgxpool
// for the unit suite.
type anchorsExportSource interface {
	listAnchors(ctx context.Context, limit int) ([]anchorExportRow, bool, error)
	listEdges(ctx context.Context) ([]edgeExportRow, error)
}

// anchorExportRow is the wire-ready projection of one anchor row.
type anchorExportRow struct {
	ID                 uuid.UUID
	SCFID              string
	Family             string
	Title              string
	Description        string
	FrameworkVersionID uuid.UUID
	FrameworkVersion   string
	FrameworkSlug      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// edgeExportRow is one anchor → framework requirement edge.
type edgeExportRow struct {
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

// NewExportHandler constructs the slice 174 export handler.
func NewExportHandler(pool *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{pool: pool}
}

// WithSource installs an anchorsExportSource for tests. Production
// callers leave it nil — the handler falls back to the inline pool-
// backed adapter.
func (h *ExportHandler) WithSource(s anchorsExportSource) *ExportHandler {
	h.source = s
	return h
}

// WithLimiter installs a Limiter into the handler.
func (h *ExportHandler) WithLimiter(l *export.Limiter) *ExportHandler {
	h.limiter = l
	return h
}

// ExportAnchors handles `GET /v1/anchors/export?format=...`.
func (h *ExportHandler) ExportAnchors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Role / credential gate (defense-in-depth). The upstream OPA
	// middleware is the primary gate; this layer extracts the caller
	// identity for the meta-audit row. Missing credential -> 401.
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		writeExportError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeExportError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	// Parse + validate the format param.
	format, formatErr := parseAnchorsExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		writeExportError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	// Slice 145 — per-(tenant, user) concurrency cap.
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
			Format: string(format),
			Result: "denied:concurrency_cap_exceeded",
			Reason: capErr.Error(),
		})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf(
				"export concurrency cap (%d) reached for this (tenant, user); "+
					"retry in 30s",
				limiter.Cap()),
			"retry_after_seconds": 30,
			"cap":                 limiter.Cap(),
		})
		return
	}
	defer release()

	// Pull anchors. Ask for one more than the cap so we can detect
	// overflow without a separate count query.
	rowCap := defaultAnchorsExportRowCap
	anchors, exceededCap, err := h.listAnchorsFor(ctx, rowCap+1)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: err.Error(),
		})
		writeExportError(w, http.StatusInternalServerError, "list anchors for export: "+err.Error())
		return
	}
	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(anchors),
		})
		writeExportError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d anchors; "+
				"contact the maintainer if the SCF catalog legitimately exceeds this ceiling",
				rowCap))
		return
	}

	// Pull edges (used by all three projections). The edges list is
	// pulled fully because edges-per-anchor fan-out is bounded
	// (~3–8) and the catalog total is ~10K rows.
	edges, err := h.listEdgesFor(ctx)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: err.Error(),
		})
		writeExportError(w, http.StatusInternalServerError, "list edges for export: "+err.Error())
		return
	}

	// Group edges by anchor id for the JSON + CSV nested projections.
	edgesByAnchor := groupEdgesByAnchor(edges)

	// Resolve filename + Content-Type per format.
	ext := string(format)
	contentType := contentTypeFor(format)
	filename := export.BuildFilename(anchorsExportEntity, ext, nil)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &anchorsCountingWriter{w: w}
	switch format {
	case export.FormatCSV:
		err = writeAnchorsCSV(cw, anchors, edgesByAnchor)
	case export.FormatJSON:
		err = writeAnchorsJSON(cw, anchors, edgesByAnchor)
	case export.FormatXLSX:
		err = writeAnchorsXLSX(cw, anchors, edges)
	default:
		err = fmt.Errorf("unsupported format %q", format)
	}
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    err.Error(),
			RowCount:  len(anchors),
			ByteCount: cw.n,
		})
		return
	}

	h.writeMetaAudit(ctx, tenantID, userIdentifier, anchorsExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(anchors),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

func parseAnchorsExportFormat(r *http.Request) (export.Format, error) {
	raw := r.URL.Query().Get("format")
	if raw == "" {
		raw = string(export.FormatCSV)
	}
	format := export.Format(strings.ToLower(raw))
	if !export.IsValid(format) {
		return format, fmt.Errorf("unsupported format %q (want csv|json|xlsx)", raw)
	}
	return format, nil
}

func contentTypeFor(f export.Format) string {
	switch f {
	case export.FormatCSV:
		return "text/csv; charset=utf-8"
	case export.FormatJSON:
		return "application/json"
	case export.FormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "application/octet-stream"
}

// ===== Header definitions (locked) =====

// anchorsExportSheet1Header is the column order for the Anchors sheet
// (XLSX) and the flat-nested row in CSV. Anchor-metadata columns plus
// the `framework_satisfactions` JSON column at the tail (CSV only;
// XLSX Sheet 1 omits it — the join lives on Sheet 2).
//
// Locked: changing this list is a breaking change for downstream
// consumers (auditor-handoff index sheets keyed off column position).
func anchorsExportSheet1Header() []string {
	return []string{
		"id",
		"scf_id",
		"family",
		"title",
		"description",
		"framework_version_id",
		"framework_version",
		"framework_slug",
		"created_at",
		"updated_at",
	}
}

// anchorsExportCSVHeader is anchorsExportSheet1Header plus the
// trailing `framework_satisfactions` JSON column for the CSV
// projection.
func anchorsExportCSVHeader() []string {
	base := anchorsExportSheet1Header()
	return append(base, "framework_satisfactions")
}

// anchorsExportEdgesHeader is the column order for the Edges sheet
// (XLSX). Join keys first so VLOOKUP / XLOOKUP land on column A.
func anchorsExportEdgesHeader() []string {
	return []string{
		"anchor_id",
		"anchor_scf_id",
		"edge_id",
		"framework_requirement_id",
		"framework_requirement_code",
		"framework_requirement_title",
		"framework_slug",
		"framework_version",
		"relationship_type",
		"strength",
		"source_attribution",
		"rationale",
	}
}

// ===== CSV projection (flat-nested fallback) =====

// writeAnchorsCSV emits one row per anchor with framework satisfactions
// JSON-stringified into the last column. Reuses the slice 135 CSV
// encoder (which applies the OWASP cell-injection sanitizer to every
// cell, including the JSON column).
func writeAnchorsCSV(w io.Writer, anchors []anchorExportRow, edgesByAnchor map[uuid.UUID][]edgeExportRow) error {
	enc := export.NewCSVExporter()
	header := anchorsExportCSVHeader()
	return enc.WriteRows(w, header, anchorsCSVRowIter(anchors, edgesByAnchor))
}

// anchorsCSVRowIter projects anchors into the CSV row shape. The
// `framework_satisfactions` cell is a JSON-stringified array. The
// JSON starts with `[` — NOT a CSV-formula introducer — so the
// sanitizer's check is a no-op for this cell.
func anchorsCSVRowIter(anchors []anchorExportRow, edgesByAnchor map[uuid.UUID][]edgeExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, a := range anchors {
			satsJSON, err := encodeSatisfactionsForAnchor(a.ID, edgesByAnchor[a.ID])
			if err != nil {
				// Failed JSON encode -> emit empty array so the row
				// still has the canonical column count. Real data
				// values never produce encode errors in practice;
				// this is defensive.
				satsJSON = "[]"
			}
			row := []string{
				a.ID.String(),
				a.SCFID,
				a.Family,
				a.Title,
				a.Description,
				a.FrameworkVersionID.String(),
				a.FrameworkVersion,
				a.FrameworkSlug,
				a.CreatedAt.UTC().Format(time.RFC3339),
				a.UpdatedAt.UTC().Format(time.RFC3339),
				satsJSON,
			}
			if !yield(row) {
				return
			}
		}
	}
}

// ===== JSON projection (nested) =====

// satisfactionWire is the per-edge nested shape in the JSON
// projection. Field names mirror the column header set on the
// XLSX Edges sheet — same data, different format.
type satisfactionWire struct {
	EdgeID                    string  `json:"edge_id"`
	FrameworkRequirementID    string  `json:"framework_requirement_id"`
	FrameworkRequirementCode  string  `json:"framework_requirement_code"`
	FrameworkRequirementTitle string  `json:"framework_requirement_title"`
	FrameworkSlug             string  `json:"framework_slug"`
	FrameworkVersion          string  `json:"framework_version"`
	RelationshipType          string  `json:"relationship_type"`
	Strength                  float64 `json:"strength"`
	SourceAttribution         string  `json:"source_attribution"`
	Rationale                 string  `json:"rationale,omitempty"`
}

// nestedAnchorWire is one anchor with its framework satisfactions
// inline. The shape mirrors the slice 098 anchor wire type plus the
// satisfactions array; consumers comparing JSON-API responses can
// recognize the fields.
type nestedAnchorWire struct {
	ID                     string             `json:"id"`
	SCFID                  string             `json:"scf_id"`
	Family                 string             `json:"family"`
	Title                  string             `json:"title"`
	Description            string             `json:"description"`
	FrameworkVersionID     string             `json:"framework_version_id"`
	FrameworkVersion       string             `json:"framework_version"`
	FrameworkSlug          string             `json:"framework_slug"`
	CreatedAt              string             `json:"created_at"`
	UpdatedAt              string             `json:"updated_at"`
	FrameworkSatisfactions []satisfactionWire `json:"framework_satisfactions"`
}

// writeAnchorsJSON emits a JSON array of nested anchor objects.
// Stream-built one anchor at a time; no full-array buffer.
func writeAnchorsJSON(w io.Writer, anchors []anchorExportRow, edgesByAnchor map[uuid.UUID][]edgeExportRow) error {
	if _, err := io.WriteString(w, "["); err != nil {
		return fmt.Errorf("json open: %w", err)
	}
	first := true
	for _, a := range anchors {
		obj := nestedAnchorWire{
			ID:                     a.ID.String(),
			SCFID:                  a.SCFID,
			Family:                 a.Family,
			Title:                  a.Title,
			Description:            a.Description,
			FrameworkVersionID:     a.FrameworkVersionID.String(),
			FrameworkVersion:       a.FrameworkVersion,
			FrameworkSlug:          a.FrameworkSlug,
			CreatedAt:              a.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:              a.UpdatedAt.UTC().Format(time.RFC3339),
			FrameworkSatisfactions: satisfactionsForAnchor(edgesByAnchor[a.ID]),
		}
		blob, err := json.Marshal(obj)
		if err != nil {
			return fmt.Errorf("json marshal anchor %s: %w", a.ID, err)
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return fmt.Errorf("json comma: %w", err)
			}
		}
		first = false
		if _, err := w.Write(blob); err != nil {
			return fmt.Errorf("json write anchor: %w", err)
		}
	}
	if _, err := io.WriteString(w, "]"); err != nil {
		return fmt.Errorf("json close: %w", err)
	}
	return nil
}

// satisfactionsForAnchor materializes the per-anchor satisfactions
// array. `nil` returns an empty slice (not nil) so the JSON renders
// `[]` rather than `null`.
func satisfactionsForAnchor(edges []edgeExportRow) []satisfactionWire {
	out := make([]satisfactionWire, 0, len(edges))
	for _, e := range edges {
		out = append(out, satisfactionWire{
			EdgeID:                    e.EdgeID.String(),
			FrameworkRequirementID:    e.FrameworkRequirementID.String(),
			FrameworkRequirementCode:  e.FrameworkRequirementCode,
			FrameworkRequirementTitle: e.FrameworkRequirementTitle,
			FrameworkSlug:             e.FrameworkSlug,
			FrameworkVersion:          e.FrameworkVersion,
			RelationshipType:          e.RelationshipType,
			Strength:                  e.Strength,
			SourceAttribution:         e.SourceAttribution,
			Rationale:                 e.Rationale,
		})
	}
	return out
}

// encodeSatisfactionsForAnchor returns the JSON-stringified array
// used in the CSV projection. The empty-array case renders as `[]`.
func encodeSatisfactionsForAnchor(_ uuid.UUID, edges []edgeExportRow) (string, error) {
	sats := satisfactionsForAnchor(edges)
	blob, err := json.Marshal(sats)
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

// groupEdgesByAnchor partitions a flat slice of edges into a map
// keyed by anchor_id. Order is preserved within each anchor's group.
func groupEdgesByAnchor(edges []edgeExportRow) map[uuid.UUID][]edgeExportRow {
	out := make(map[uuid.UUID][]edgeExportRow)
	for _, e := range edges {
		out[e.AnchorID] = append(out[e.AnchorID], e)
	}
	return out
}

// ===== XLSX projection (two-sheet) =====
//
// Slice 174 D6 — the two-sheet writer lives in this package and uses
// archive/zip + encoding/xml directly. The generic
// `internal/export/xlsx.go` is single-sheet by construction; reusing
// it would require API churn. The slice 135 P0-A6 anti-criterion
// (no charts / named ranges / VBA / formatting) is satisfied BY
// CONSTRUCTION here: the code paths for those zip members do not
// exist. Test pins the exact zip-member list.

// writeAnchorsXLSX writes a minimal two-sheet text-only .xlsx zip.
//
// Zip member list (exactly 6 entries):
//
//	[Content_Types].xml
//	_rels/.rels
//	xl/workbook.xml                 (two <sheet> entries)
//	xl/_rels/workbook.xml.rels      (two relationships)
//	xl/worksheets/sheet1.xml        (Anchors)
//	xl/worksheets/sheet2.xml        (Edges)
//
// Inline-string cells avoid needing `xl/sharedStrings.xml`.
func writeAnchorsXLSX(w io.Writer, anchors []anchorExportRow, edges []edgeExportRow) error {
	zw := zip.NewWriter(w)

	if err := writeAnchorsZipFile(zw, "[Content_Types].xml", anchorsContentTypesXML); err != nil {
		return err
	}
	if err := writeAnchorsZipFile(zw, "_rels/.rels", anchorsRootRelsXML); err != nil {
		return err
	}
	if err := writeAnchorsZipFile(zw, "xl/workbook.xml", anchorsWorkbookXML); err != nil {
		return err
	}
	if err := writeAnchorsZipFile(zw, "xl/_rels/workbook.xml.rels", anchorsWorkbookRelsXML); err != nil {
		return err
	}

	// Sheet 1 — Anchors
	sheet1Writer, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return fmt.Errorf("xlsx: create sheet1: %w", err)
	}
	if err := writeAnchorsSheet1(sheet1Writer, anchors); err != nil {
		return err
	}

	// Sheet 2 — Edges
	sheet2Writer, err := zw.Create("xl/worksheets/sheet2.xml")
	if err != nil {
		return fmt.Errorf("xlsx: create sheet2: %w", err)
	}
	if err := writeAnchorsSheet2(sheet2Writer, edges); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("xlsx: zip close: %w", err)
	}
	return nil
}

// writeAnchorsSheet1 emits Sheet 1 (Anchors). Stream-pumps one
// anchor at a time; no full-result buffer.
func writeAnchorsSheet1(w io.Writer, anchors []anchorExportRow) error {
	if _, err := io.WriteString(w, anchorsSheetPrologue); err != nil {
		return fmt.Errorf("xlsx: sheet1 prologue: %w", err)
	}
	header := anchorsExportSheet1Header()
	rowIndex := 1
	if err := writeAnchorsSheetRow(w, rowIndex, header); err != nil {
		return err
	}
	rowIndex++
	for _, a := range anchors {
		cells := []string{
			a.ID.String(),
			a.SCFID,
			a.Family,
			a.Title,
			a.Description,
			a.FrameworkVersionID.String(),
			a.FrameworkVersion,
			a.FrameworkSlug,
			a.CreatedAt.UTC().Format(time.RFC3339),
			a.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if err := writeAnchorsSheetRow(w, rowIndex, cells); err != nil {
			return err
		}
		rowIndex++
	}
	if _, err := io.WriteString(w, anchorsSheetEpilogue); err != nil {
		return fmt.Errorf("xlsx: sheet1 epilogue: %w", err)
	}
	return nil
}

// writeAnchorsSheet2 emits Sheet 2 (Edges).
func writeAnchorsSheet2(w io.Writer, edges []edgeExportRow) error {
	if _, err := io.WriteString(w, anchorsSheetPrologue); err != nil {
		return fmt.Errorf("xlsx: sheet2 prologue: %w", err)
	}
	header := anchorsExportEdgesHeader()
	rowIndex := 1
	if err := writeAnchorsSheetRow(w, rowIndex, header); err != nil {
		return err
	}
	rowIndex++
	for _, e := range edges {
		cells := []string{
			e.AnchorID.String(),
			e.AnchorSCFID,
			e.EdgeID.String(),
			e.FrameworkRequirementID.String(),
			e.FrameworkRequirementCode,
			e.FrameworkRequirementTitle,
			e.FrameworkSlug,
			e.FrameworkVersion,
			e.RelationshipType,
			strconv.FormatFloat(e.Strength, 'f', -1, 64),
			e.SourceAttribution,
			e.Rationale,
		}
		if err := writeAnchorsSheetRow(w, rowIndex, cells); err != nil {
			return err
		}
		rowIndex++
	}
	if _, err := io.WriteString(w, anchorsSheetEpilogue); err != nil {
		return fmt.Errorf("xlsx: sheet2 epilogue: %w", err)
	}
	return nil
}

// writeAnchorsSheetRow emits one <row> element with inline-string
// cells. Cell reference style is A1 notation (column letters from
// the zero-based index).
func writeAnchorsSheetRow(w io.Writer, rowIndex int, cells []string) error {
	var b strings.Builder
	b.WriteString(`<row r="`)
	b.WriteString(strconv.Itoa(rowIndex))
	b.WriteString(`">`)
	for i, cell := range cells {
		ref := anchorsColLetters(i) + strconv.Itoa(rowIndex)
		b.WriteString(`<c r="`)
		b.WriteString(ref)
		b.WriteString(`" t="inlineStr"><is><t xml:space="preserve">`)
		var esc strings.Builder
		_ = xml.EscapeText(&anchorsEscWriter{b: &esc}, []byte(cell))
		b.WriteString(esc.String())
		b.WriteString(`</t></is></c>`)
	}
	b.WriteString(`</row>`)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("xlsx: write row %d: %w", rowIndex, err)
	}
	return nil
}

// anchorsEscWriter pipes xml.EscapeText output into a Builder.
type anchorsEscWriter struct{ b *strings.Builder }

func (e *anchorsEscWriter) Write(p []byte) (int, error) {
	e.b.Write(p)
	return len(p), nil
}

// anchorsColLetters converts a zero-based column index to A1-style
// column letters (0 -> "A", 25 -> "Z", 26 -> "AA", …).
func anchorsColLetters(zeroIdx int) string {
	n := zeroIdx + 1
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('A' + (n % 26))}, out...)
		n /= 26
	}
	return string(out)
}

// writeAnchorsZipFile writes a single zip member with the given
// path and body.
func writeAnchorsZipFile(zw *zip.Writer, path string, body string) error {
	w, err := zw.Create(path)
	if err != nil {
		return fmt.Errorf("xlsx: create %s: %w", path, err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("xlsx: write %s: %w", path, err)
	}
	return nil
}

// ===== Static XML fragments (two-sheet) =====

const anchorsContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`</Types>`

const anchorsRootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

// Two <sheet> entries — Anchors (sheetId=1, r:id=rId1) and Edges
// (sheetId=2, r:id=rId2). No defined-name block; no chart references.
const anchorsWorkbookXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
	`<sheets>` +
	`<sheet name="Anchors" sheetId="1" r:id="rId1"/>` +
	`<sheet name="Edges" sheetId="2" r:id="rId2"/>` +
	`</sheets>` +
	`</workbook>`

// Two relationships — sheet1 + sheet2. No chart relationship, no
// vbaProject relationship, no theme.
const anchorsWorkbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>` +
	`</Relationships>`

const anchorsSheetPrologue = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<sheetData>`

const anchorsSheetEpilogue = `</sheetData></worksheet>`

// ===== Data access =====

// listAnchorsFor returns up to `limit` anchors. The boolean is true
// when the underlying query returned at least `limit` rows (signal
// that the caller asked cap+1 and the result hit cap+1, i.e.
// cap-exceeded).
func (h *ExportHandler) listAnchorsFor(ctx context.Context, limit int) ([]anchorExportRow, bool, error) {
	if h.source != nil {
		return h.source.listAnchors(ctx, limit)
	}
	if h.pool == nil {
		return nil, false, fmt.Errorf("anchors export: pool not wired")
	}
	q := dbx.New(h.pool)
	dbRows, err := q.ListAllSCFAnchorsForExport(ctx, int32(limit))
	if err != nil {
		return nil, false, fmt.Errorf("list all scf anchors for export: %w", err)
	}
	exceeded := len(dbRows) >= limit
	if exceeded {
		dbRows = dbRows[:limit]
	}
	out := make([]anchorExportRow, len(dbRows))
	for i, r := range dbRows {
		out[i] = anchorExportRow{
			ID:                 uuid.UUID(r.ID.Bytes),
			SCFID:              r.ScfID,
			Family:             r.Family,
			Title:              r.Title,
			Description:        r.Description,
			FrameworkVersionID: uuid.UUID(r.FrameworkVersionID.Bytes),
			FrameworkVersion:   r.FrameworkVersion,
			FrameworkSlug:      r.FrameworkSlug,
		}
		if r.CreatedAt.Valid {
			out[i].CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			out[i].UpdatedAt = r.UpdatedAt.Time
		}
	}
	return out, exceeded, nil
}

// listEdgesFor returns every edge in the current SCF catalog.
func (h *ExportHandler) listEdgesFor(ctx context.Context) ([]edgeExportRow, error) {
	if h.source != nil {
		return h.source.listEdges(ctx)
	}
	if h.pool == nil {
		return nil, fmt.Errorf("anchors export: pool not wired")
	}
	q := dbx.New(h.pool)
	dbRows, err := q.ListAllFwToScfEdgesForExport(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all fw_to_scf edges for export: %w", err)
	}
	out := make([]edgeExportRow, len(dbRows))
	for i, r := range dbRows {
		out[i] = edgeExportRow{
			EdgeID:                    uuid.UUID(r.EdgeID.Bytes),
			AnchorID:                  uuid.UUID(r.AnchorID.Bytes),
			AnchorSCFID:               r.AnchorScfID,
			FrameworkRequirementID:    uuid.UUID(r.FrameworkRequirementID.Bytes),
			FrameworkRequirementCode:  r.FrameworkRequirementCode,
			FrameworkRequirementTitle: r.FrameworkRequirementTitle,
			FrameworkSlug:             r.FrameworkSlug,
			FrameworkVersion:          r.FrameworkVersion,
			RelationshipType:          string(r.RelationshipType),
			Strength:                  r.Strength,
			SourceAttribution:         string(r.SourceAttribution),
			Rationale:                 r.Rationale,
		}
	}
	return out, nil
}

// ===== Meta-audit =====

// anchorsExportMetaAudit is the JSON shape persisted to
// `me_audit_log.after` on every export attempt. Outcome buckets
// mirror the slice 135 / 137 / 138 / 139 convention.
type anchorsExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

func (h *ExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta anchorsExportMetaAudit) {
	if h.pool == nil {
		return
	}
	paramsBlob, _ := json.Marshal(map[string]any{
		"format": meta.Format,
	})
	resultBlob, _ := json.Marshal(meta)

	uID, parseErr := uuid.Parse(userIdentifier)
	if parseErr != nil {
		uID = uuid.Nil
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		return
	}
	q := dbx.New(tx)
	if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: uID, Valid: true},
		Action:   metaAuditActionAnchorsExport,
		Before:   paramsBlob,
		After:    resultBlob,
	}); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// exportLimiter returns the slice 145 per-(tenant, user) concurrency
// limiter for this handler. Default returns the process-wide
// singleton; tests override via WithLimiter.
func (h *ExportHandler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// ===== Counting writer =====

type anchorsCountingWriter struct {
	w io.Writer
	n int64
}

func (c *anchorsCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// ===== Local helpers =====

// writeExportError writes a JSON error body with the given status.
// Local helper so the export handler does not depend on the
// `internal/api/anchors.writeError` (which is wired to a different
// response shape — the catalog read endpoints use a different
// envelope).
func writeExportError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
