// Slice 174 — unit tests for the anchors catalog export handler.
//
// The integration suite exercises the full wire surface against
// Postgres (build-tag `integration`); this file covers the pure
// projection functions + the handler dispatch's early-exit branches
// + the format-specific writers against in-memory rows.

package anchors

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/export"
)

// stubAnchorsSource is a deterministic implementation of
// anchorsExportSource for unit-suite use.
type stubAnchorsSource struct {
	anchors  []anchorExportRow
	edges    []edgeExportRow
	exceeded bool
	listErr  error
	edgesErr error
}

func (s *stubAnchorsSource) listAnchors(_ context.Context, _ int) ([]anchorExportRow, bool, error) {
	return s.anchors, s.exceeded, s.listErr
}

func (s *stubAnchorsSource) listEdges(_ context.Context) ([]edgeExportRow, error) {
	return s.edges, s.edgesErr
}

// TestSlice174_DefaultRowCap pins the row-cap default at 50,000.
func TestSlice174_DefaultRowCap(t *testing.T) {
	if defaultAnchorsExportRowCap != 50_000 {
		t.Errorf("defaultAnchorsExportRowCap = %d; want 50000 (slice 174 D3)",
			defaultAnchorsExportRowCap)
	}
}

// TestSlice174_MetaAuditActionConstant locks the plural meta-audit
// action value.
func TestSlice174_MetaAuditActionConstant(t *testing.T) {
	if metaAuditActionAnchorsExport != "anchors_export" {
		t.Errorf("metaAuditActionAnchorsExport = %q; want %q (slice 174 D2 plural convention)",
			metaAuditActionAnchorsExport, "anchors_export")
	}
}

// TestSlice174_ParseAnchorsExportFormat verifies the URL parsing.
func TestSlice174_ParseAnchorsExportFormat(t *testing.T) {
	cases := []struct {
		query     string
		want      export.Format
		expectErr bool
	}{
		{"", export.FormatCSV, false},
		{"format=csv", export.FormatCSV, false},
		{"format=json", export.FormatJSON, false},
		{"format=xlsx", export.FormatXLSX, false},
		{"format=CSV", export.FormatCSV, false},
		{"format=pdf", "pdf", true},
		{"format=", export.FormatCSV, false},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/anchors/export?"+tc.query, nil)
			got, err := parseAnchorsExportFormat(req)
			if tc.expectErr {
				if err == nil {
					t.Errorf("want error; got format=%q err=nil", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("format = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestSlice174_AnchorsExportSheet1Header_StableOrder locks the
// canonical column order for Sheet 1.
func TestSlice174_AnchorsExportSheet1Header_StableOrder(t *testing.T) {
	want := []string{
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
	got := anchorsExportSheet1Header()
	if len(got) != len(want) {
		t.Fatalf("column count = %d; want %d", len(got), len(want))
	}
	for i, c := range want {
		if got[i] != c {
			t.Errorf("column[%d] = %q; want %q", i, got[i], c)
		}
	}
}

// TestSlice174_AnchorsExportEdgesHeader_StableOrder locks the
// canonical column order for Sheet 2 (Edges). Join keys
// (anchor_id, anchor_scf_id) MUST sit at columns A and B per D8.
func TestSlice174_AnchorsExportEdgesHeader_StableOrder(t *testing.T) {
	want := []string{
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
	got := anchorsExportEdgesHeader()
	if len(got) != len(want) {
		t.Fatalf("column count = %d; want %d", len(got), len(want))
	}
	for i, c := range want {
		if got[i] != c {
			t.Errorf("column[%d] = %q; want %q", i, got[i], c)
		}
	}
}

// TestSlice174_AnchorsExportCSVHeader_TrailingSatisfactionsColumn
// pins the JSON-stringified column at the tail of the CSV row.
func TestSlice174_AnchorsExportCSVHeader_TrailingSatisfactionsColumn(t *testing.T) {
	h := anchorsExportCSVHeader()
	if len(h) == 0 {
		t.Fatal("empty header")
	}
	last := h[len(h)-1]
	if last != "framework_satisfactions" {
		t.Errorf("last column = %q; want %q", last, "framework_satisfactions")
	}
}

// TestSlice174_GroupEdgesByAnchor partitions a flat slice of edges
// into a map keyed by anchor_id, preserving order within each group.
func TestSlice174_GroupEdgesByAnchor(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	edges := []edgeExportRow{
		{EdgeID: uuid.New(), AnchorID: a, FrameworkRequirementCode: "CC1.1"},
		{EdgeID: uuid.New(), AnchorID: b, FrameworkRequirementCode: "CC2.1"},
		{EdgeID: uuid.New(), AnchorID: a, FrameworkRequirementCode: "CC1.2"},
		{EdgeID: uuid.New(), AnchorID: b, FrameworkRequirementCode: "CC2.2"},
	}
	grouped := groupEdgesByAnchor(edges)
	if len(grouped) != 2 {
		t.Fatalf("group count = %d; want 2", len(grouped))
	}
	if len(grouped[a]) != 2 || len(grouped[b]) != 2 {
		t.Errorf("expected 2 edges per anchor; got a=%d b=%d", len(grouped[a]), len(grouped[b]))
	}
	if grouped[a][0].FrameworkRequirementCode != "CC1.1" || grouped[a][1].FrameworkRequirementCode != "CC1.2" {
		t.Errorf("anchor a edges out of order: %+v", grouped[a])
	}
}

// TestSlice174_SatisfactionsForAnchor_EmptyRendersEmptySlice ensures
// the empty case renders as `[]` not `null` in JSON.
func TestSlice174_SatisfactionsForAnchor_EmptyRendersEmptySlice(t *testing.T) {
	got := satisfactionsForAnchor(nil)
	if got == nil {
		t.Error("got nil; want empty slice (so JSON renders [] not null)")
	}
	if len(got) != 0 {
		t.Errorf("len = %d; want 0", len(got))
	}
}

// TestSlice174_AnchorsColLetters tests the A1 column-letter mapping.
func TestSlice174_AnchorsColLetters(t *testing.T) {
	cases := map[int]string{
		0:  "A",
		1:  "B",
		25: "Z",
		26: "AA",
		27: "AB",
		51: "AZ",
		52: "BA",
	}
	for idx, want := range cases {
		got := anchorsColLetters(idx)
		if got != want {
			t.Errorf("anchorsColLetters(%d) = %q; want %q", idx, got, want)
		}
	}
}

// TestSlice174_ContentTypeFor pins the Content-Type per format.
func TestSlice174_ContentTypeFor(t *testing.T) {
	cases := map[export.Format]string{
		export.FormatCSV:  "text/csv; charset=utf-8",
		export.FormatJSON: "application/json",
		export.FormatXLSX: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
	for f, want := range cases {
		if got := contentTypeFor(f); got != want {
			t.Errorf("contentTypeFor(%q) = %q; want %q", f, got, want)
		}
	}
}

// TestSlice174_WriteAnchorsCSV_FlatNestedShape verifies the flat CSV
// shape with the JSON-stringified satisfactions column.
func TestSlice174_WriteAnchorsCSV_FlatNestedShape(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	a1 := uuid.New()
	a2 := uuid.New()
	anchors := []anchorExportRow{
		{ID: a1, SCFID: "IAC-06", Family: "iam", Title: "MFA", Description: "force MFA", CreatedAt: now, UpdatedAt: now, FrameworkSlug: "scf", FrameworkVersion: "2026.1"},
		{ID: a2, SCFID: "IAC-07", Family: "iam", Title: "PWD", Description: "password policy", CreatedAt: now, UpdatedAt: now, FrameworkSlug: "scf", FrameworkVersion: "2026.1"},
	}
	edges := map[uuid.UUID][]edgeExportRow{
		a1: {
			{EdgeID: uuid.New(), AnchorID: a1, AnchorSCFID: "IAC-06", FrameworkSlug: "soc2", FrameworkVersion: "2017", FrameworkRequirementCode: "CC6.6", RelationshipType: "equal", Strength: 1.0, SourceAttribution: "scf_official"},
		},
	}
	var buf bytes.Buffer
	if err := writeAnchorsCSV(&buf, anchors, edges); err != nil {
		t.Fatalf("writeAnchorsCSV: %v", err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) != 3 { // header + 2 anchors
		t.Fatalf("rows = %d; want 3", len(rows))
	}
	header := rows[0]
	if header[0] != "id" || header[len(header)-1] != "framework_satisfactions" {
		t.Errorf("CSV header = %v; want id...framework_satisfactions", header)
	}
	// Row 1 has one satisfaction; row 2 has zero -> `[]`.
	sats1 := rows[1][len(header)-1]
	sats2 := rows[2][len(header)-1]
	if !strings.Contains(sats1, "CC6.6") {
		t.Errorf("row 1 satisfactions = %q; want substring CC6.6", sats1)
	}
	if sats2 != "[]" {
		t.Errorf("row 2 satisfactions = %q; want []", sats2)
	}
}

// TestSlice174_WriteAnchorsJSON_NestedShape verifies the nested JSON
// shape — one object per anchor with `framework_satisfactions` array.
func TestSlice174_WriteAnchorsJSON_NestedShape(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	a1 := uuid.New()
	anchors := []anchorExportRow{
		{ID: a1, SCFID: "IAC-06", Family: "iam", Title: "MFA", Description: "force MFA", CreatedAt: now, UpdatedAt: now, FrameworkSlug: "scf", FrameworkVersion: "2026.1"},
	}
	edges := map[uuid.UUID][]edgeExportRow{
		a1: {
			{EdgeID: uuid.New(), AnchorID: a1, AnchorSCFID: "IAC-06", FrameworkSlug: "soc2", FrameworkVersion: "2017", FrameworkRequirementCode: "CC6.6", RelationshipType: "equal", Strength: 1.0, SourceAttribution: "scf_official"},
		},
	}
	var buf bytes.Buffer
	if err := writeAnchorsJSON(&buf, anchors, edges); err != nil {
		t.Fatalf("writeAnchorsJSON: %v", err)
	}
	var parsed []nestedAnchorWire
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, buf.String())
	}
	if len(parsed) != 1 {
		t.Fatalf("anchor count = %d; want 1", len(parsed))
	}
	if parsed[0].SCFID != "IAC-06" {
		t.Errorf("scf_id = %q; want IAC-06", parsed[0].SCFID)
	}
	if len(parsed[0].FrameworkSatisfactions) != 1 {
		t.Fatalf("satisfaction count = %d; want 1", len(parsed[0].FrameworkSatisfactions))
	}
	if parsed[0].FrameworkSatisfactions[0].FrameworkRequirementCode != "CC6.6" {
		t.Errorf("satisfaction code = %q; want CC6.6", parsed[0].FrameworkSatisfactions[0].FrameworkRequirementCode)
	}
}

// TestSlice174_WriteAnchorsJSON_EmptySatisfactionsRendersEmptyArray
// — the per-anchor `framework_satisfactions` is `[]` (not null) when
// the anchor has no edges. Downstream consumers (jq, scripts) treat
// `[]` as "empty list" while `null` is "field absent" — different
// semantics; the contract is `[]`.
func TestSlice174_WriteAnchorsJSON_EmptySatisfactionsRendersEmptyArray(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	a1 := uuid.New()
	anchors := []anchorExportRow{
		{ID: a1, SCFID: "IAC-06", Family: "iam", Title: "MFA", CreatedAt: now, UpdatedAt: now, FrameworkSlug: "scf"},
	}
	var buf bytes.Buffer
	if err := writeAnchorsJSON(&buf, anchors, nil); err != nil {
		t.Fatalf("writeAnchorsJSON: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `"framework_satisfactions":[]`) {
		t.Errorf("body does NOT contain `\"framework_satisfactions\":[]`; got %s", body)
	}
}

// TestSlice174_WriteAnchorsXLSX_ZipMemberList_SixFiles is the
// P0-A-174-2 enforcement test — the two-sheet XLSX MUST emit
// EXACTLY 6 zip members (no chart objects, no named ranges, no
// VBA, no shared strings, no themes). Mirrors the slice 135
// AC-4 fixture pinning.
func TestSlice174_WriteAnchorsXLSX_ZipMemberList_SixFiles(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	a1 := uuid.New()
	anchors := []anchorExportRow{
		{ID: a1, SCFID: "IAC-06", Family: "iam", Title: "MFA", CreatedAt: now, UpdatedAt: now, FrameworkSlug: "scf"},
	}
	edges := []edgeExportRow{
		{EdgeID: uuid.New(), AnchorID: a1, AnchorSCFID: "IAC-06", FrameworkSlug: "soc2", FrameworkVersion: "2017", FrameworkRequirementCode: "CC6.6", RelationshipType: "equal", Strength: 1.0, SourceAttribution: "scf_official"},
	}
	var buf bytes.Buffer
	if err := writeAnchorsXLSX(&buf, anchors, edges); err != nil {
		t.Fatalf("writeAnchorsXLSX: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	wantMembers := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/worksheets/sheet1.xml",
		"xl/worksheets/sheet2.xml",
	}
	if len(zr.File) != len(wantMembers) {
		gotNames := make([]string, len(zr.File))
		for i, f := range zr.File {
			gotNames[i] = f.Name
		}
		t.Fatalf("zip member count = %d; want %d; got=%v want=%v",
			len(zr.File), len(wantMembers), gotNames, wantMembers)
	}
	// Set equality (order is implementation-dependent in archive/zip).
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	for _, want := range wantMembers {
		if !got[want] {
			t.Errorf("zip is missing required member %q", want)
		}
	}
	// Anti-criterion checks: absolutely no chart / named-range / VBA
	// members. These would be supply-chain risk for a downstream
	// auditor parsing the file with a permissive XLSX reader.
	forbiddenSubstrings := []string{
		"xl/charts/",
		"xl/vbaProject.bin",
		"xl/definedNames",
		"docProps/",
	}
	for _, f := range zr.File {
		for _, banned := range forbiddenSubstrings {
			if strings.Contains(f.Name, banned) {
				t.Errorf("zip contains forbidden member %q (matches %q) — P0-A-174-2 violation", f.Name, banned)
			}
		}
	}
}

// TestSlice174_WriteAnchorsXLSX_TwoSheetsCarryHeaders verifies both
// sheets emit their header rows.
func TestSlice174_WriteAnchorsXLSX_TwoSheetsCarryHeaders(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	a1 := uuid.New()
	anchors := []anchorExportRow{
		{ID: a1, SCFID: "IAC-06", Family: "iam", Title: "MFA", CreatedAt: now, UpdatedAt: now, FrameworkSlug: "scf"},
	}
	edges := []edgeExportRow{
		{EdgeID: uuid.New(), AnchorID: a1, AnchorSCFID: "IAC-06", FrameworkSlug: "soc2", FrameworkRequirementCode: "CC6.6", RelationshipType: "equal", Strength: 1.0, SourceAttribution: "scf_official"},
	}
	var buf bytes.Buffer
	if err := writeAnchorsXLSX(&buf, anchors, edges); err != nil {
		t.Fatalf("writeAnchorsXLSX: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	readMember := func(name string) string {
		for _, f := range zr.File {
			if f.Name != name {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			return string(b)
		}
		t.Fatalf("member %q not found", name)
		return ""
	}
	sheet1 := readMember("xl/worksheets/sheet1.xml")
	if !strings.Contains(sheet1, "scf_id") || !strings.Contains(sheet1, "family") {
		t.Errorf("sheet1.xml missing anchor headers: %s", sheet1)
	}
	if !strings.Contains(sheet1, "IAC-06") {
		t.Errorf("sheet1.xml missing anchor data row: %s", sheet1)
	}
	sheet2 := readMember("xl/worksheets/sheet2.xml")
	if !strings.Contains(sheet2, "anchor_id") || !strings.Contains(sheet2, "relationship_type") {
		t.Errorf("sheet2.xml missing edges headers: %s", sheet2)
	}
	if !strings.Contains(sheet2, "CC6.6") {
		t.Errorf("sheet2.xml missing edge data row: %s", sheet2)
	}
	// The workbook.xml MUST declare BOTH sheets.
	wb := readMember("xl/workbook.xml")
	if !strings.Contains(wb, `name="Anchors"`) || !strings.Contains(wb, `name="Edges"`) {
		t.Errorf("workbook.xml does not declare two sheets named Anchors + Edges: %s", wb)
	}
}

// TestSlice174_NewExportHandler_StoresPool exercises the constructor
// + builder chain.
func TestSlice174_NewExportHandler_StoresPool(t *testing.T) {
	h := NewExportHandler(nil)
	if h == nil {
		t.Fatal("NewExportHandler returned nil")
	}
	if h.source != nil {
		t.Errorf("source default = %v; want nil", h.source)
	}
	if h.limiter != nil {
		t.Errorf("limiter default = %v; want nil", h.limiter)
	}
}

func TestSlice174_WithSource_Chains(t *testing.T) {
	h := NewExportHandler(nil)
	got := h.WithSource(&stubAnchorsSource{})
	if got != h {
		t.Errorf("WithSource returned different handler; want self-chain")
	}
	if h.source == nil {
		t.Errorf("WithSource did not install source")
	}
}

func TestSlice174_WithLimiter_Chains(t *testing.T) {
	h := NewExportHandler(nil)
	lim := export.NewLimiter(3)
	got := h.WithLimiter(lim)
	if got != h {
		t.Errorf("WithLimiter returned different handler; want self-chain")
	}
	if h.limiter != lim {
		t.Errorf("WithLimiter did not install limiter")
	}
}

func TestSlice174_ExportLimiter_FallbackAndOverride(t *testing.T) {
	t.Run("fallback", func(t *testing.T) {
		h := NewExportHandler(nil)
		if h.exportLimiter() != export.DefaultLimiter() {
			t.Errorf("default did not return DefaultLimiter()")
		}
	})
	t.Run("override", func(t *testing.T) {
		h := NewExportHandler(nil)
		lim := export.NewLimiter(7)
		h = h.WithLimiter(lim)
		if h.exportLimiter() != lim {
			t.Errorf("override not honored")
		}
	})
}

// TestSlice174_ExportAnchors_NoCredentialReturns401 is the early-exit
// path; pool not touched.
func TestSlice174_ExportAnchors_NoCredentialReturns401(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/anchors/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportAnchors(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %q; want substring 'missing credential'", rec.Body.String())
	}
}

// TestSlice174_ExportAnchors_InvalidTenantIDReturns500 is the
// other early-exit path; pool not touched.
func TestSlice174_ExportAnchors_InvalidTenantIDReturns500(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/anchors/export?format=csv", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test-id",
		TenantID: "not-a-uuid",
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportAnchors(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

// TestSlice174_AnchorsCountingWriter_CountsBytes sanity-checks the
// counting writer used to record the body byte-count for the
// meta-audit row.
func TestSlice174_AnchorsCountingWriter_CountsBytes(t *testing.T) {
	cw := &anchorsCountingWriter{w: io.Discard}
	n, err := cw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write(hello): n=%d err=%v", n, err)
	}
	n, err = cw.Write([]byte("world"))
	if err != nil || n != 5 {
		t.Fatalf("Write(world): n=%d err=%v", n, err)
	}
	if cw.n != 10 {
		t.Errorf("cw.n = %d; want 10", cw.n)
	}
}

// TestSlice174_EncodeSatisfactionsForAnchor_EmptyReturnsBracket
// confirms the empty-edge case renders as `[]` not `null`.
func TestSlice174_EncodeSatisfactionsForAnchor_EmptyReturnsBracket(t *testing.T) {
	got, err := encodeSatisfactionsForAnchor(uuid.New(), nil)
	if err != nil {
		t.Fatalf("encodeSatisfactionsForAnchor: %v", err)
	}
	if got != "[]" {
		t.Errorf("got %q; want \"[]\"", got)
	}
}
