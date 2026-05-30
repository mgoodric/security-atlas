// Slice 269 — unit tests for the dashboard snapshot export.
//
// The integration suite (`integration_test.go`, build-tag
// `integration`) exercises the full wire surface against Postgres +
// RLS; this file covers the pure functions that need no DB plus the
// early-exit handler branches that can be reached without touching
// the pgxpool.
//
// Coverage posture: `internal/api/dashboardexport/` is not in the
// CI coverage-gate per-package threshold list — like every other
// `internal/api/*export*` package, it lives in the excludes (see
// `cmd/scripts/coverage-thresholds.json`). Unit-test coverage is
// still load-bearing for fast feedback on the format dispatch +
// projection helpers.

package dashboardexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// ===== Constants + role gate =====

// AC-7 / AC-8: meta-audit action constant locked at
// `dashboard_export`. The migration CHECK extension permits exactly
// this value; a typo here would surface as a CHECK violation on
// every export attempt.
func TestSlice269_MetaAuditActionConstant(t *testing.T) {
	if metaAuditActionDashboardExport != "dashboard_export" {
		t.Errorf("metaAuditActionDashboardExport = %q; want %q",
			metaAuditActionDashboardExport, "dashboard_export")
	}
}

// AC-2: default format is `json`.
func TestSlice269_DefaultFormatIsJSON(t *testing.T) {
	if defaultFormat != FormatJSON {
		t.Errorf("defaultFormat = %q; want %q", defaultFormat, FormatJSON)
	}
}

// AC-2 / P0-A5: validFormats is exactly {json, csv, xlsx}. Any
// other set means the slice's anti-criterion has drifted.
func TestSlice269_ValidFormatsSet(t *testing.T) {
	want := map[Format]bool{
		FormatJSON: true,
		FormatCSV:  true,
		FormatXLSX: true,
	}
	if len(validFormats) != len(want) {
		t.Errorf("validFormats len = %d; want %d", len(validFormats), len(want))
	}
	for k := range want {
		if !validFormats[k] {
			t.Errorf("validFormats missing %q", k)
		}
	}
	for k := range validFormats {
		if !want[k] {
			t.Errorf("validFormats has unexpected %q", k)
		}
	}
}

// AC-6: role gate. admin OR approver only; control_owner is NOT
// admitted (the slice 066 dashboard `requireProgramRead` admits
// control_owner — slice 269 deliberately narrows that for the
// bulk-export surface).
func TestSlice269_RoleGate_PermitsAdminAndApproverOnly(t *testing.T) {
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"admin_plus_approver", credstore.Credential{IsAdmin: true, IsApprover: true}, true},
		{"control_owner_only", credstore.Credential{OwnerRoles: []string{"platform-eng"}}, false},
		{"bare_push", credstore.Credential{}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := hasDashboardExportAccess(tc.cred)
			if got != tc.want {
				t.Errorf("hasDashboardExportAccess(%+v) = %v; want %v", tc.cred, got, tc.want)
			}
		})
	}
}

// ===== parseFormat =====

func TestSlice269_ParseFormat_DefaultsAndCases(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantFmt Format
		wantErr bool
	}{
		{"empty_defaults_to_json", "", FormatJSON, false},
		{"json", "json", FormatJSON, false},
		{"csv", "csv", FormatCSV, false},
		{"xlsx", "xlsx", FormatXLSX, false},
		{"json_uppercase", "JSON", FormatJSON, false},
		{"csv_mixed_case", "Csv", FormatCSV, false},
		{"unknown_pdf_400", "pdf", Format("pdf"), true},
		{"unknown_nonsense_400", "doc", Format("doc"), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET",
				"/v1/dashboard/export?format="+tc.raw, nil)
			got, err := parseFormat(req)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseFormat(%q) err = nil; want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Errorf("parseFormat(%q) err = %v; want nil", tc.raw, err)
			}
			if got != tc.wantFmt {
				t.Errorf("parseFormat(%q) = %q; want %q", tc.raw, got, tc.wantFmt)
			}
		})
	}
}

// ===== contentMetaFor =====

func TestSlice269_ContentMetaFor_AllFormats(t *testing.T) {
	cases := []struct {
		f   Format
		ct  string
		ext string
	}{
		{FormatJSON, "application/json", "json"},
		{FormatCSV, "application/zip", "zip"},
		{FormatXLSX, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.f), func(t *testing.T) {
			ct, ext := contentMetaFor(tc.f)
			if ct != tc.ct {
				t.Errorf("ct = %q; want %q", ct, tc.ct)
			}
			if ext != tc.ext {
				t.Errorf("ext = %q; want %q", ext, tc.ext)
			}
		})
	}
}

// ===== buildFilename =====

func TestSlice269_BuildFilename_ShapeAndDate(t *testing.T) {
	got := buildFilename(dashboardExportEntity, "json")
	if !strings.HasPrefix(got, "dashboard_") {
		t.Errorf("filename = %q; want prefix %q", got, "dashboard_")
	}
	if !strings.HasSuffix(got, ".json") {
		t.Errorf("filename = %q; want suffix %q", got, ".json")
	}
	// Date segment is 8 digits.
	if len(got) != len("dashboard_20260524.json") {
		t.Errorf("filename length = %d; want %d (dashboard_YYYYMMDD.json)",
			len(got), len("dashboard_20260524.json"))
	}
}

// ===== panelOrder =====

// panelOrder is load-bearing: it drives the CSV zip-member ordering
// and the XLSX sheet ordering. Pin the exact six panels so a
// reorder surfaces as a unit-test failure.
func TestSlice269_PanelOrder_Exact(t *testing.T) {
	want := []string{
		"framework_posture",
		"risks",
		"freshness",
		"drift",
		"upcoming",
		"activity",
	}
	if len(panelOrder) != len(want) {
		t.Fatalf("panelOrder len = %d; want %d", len(panelOrder), len(want))
	}
	for i, w := range want {
		if panelOrder[i] != w {
			t.Errorf("panelOrder[%d] = %q; want %q", i, panelOrder[i], w)
		}
	}
}

// ===== encoders =====

// fixtureSnapshot is the canonical test snapshot used by the
// encoder + projection tests. Six panels populated with small
// fixed-value rows so the encoders' output is deterministic.
func fixtureSnapshot() Snapshot {
	return Snapshot{
		SnapshotAt: time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
		Panels: Panels{
			FrameworkPosture: []FrameworkPosturePanelRow{
				{
					FrameworkID:        "fw-1",
					FrameworkVersion:   "v1",
					CoveragePct:        0.87,
					FreshnessComposite: 0.92,
					TrendDelta90d:      0.05,
				},
			},
			Risks: []RiskPanelRow{
				{
					ID:            "risk-1",
					Title:         "Customer data leak",
					Treatment:     "mitigate",
					Category:      "operational",
					Methodology:   "nist_800_30",
					ResidualScore: `{"value":12}`,
					CreatedAt:     "2026-04-01T00:00:00Z",
				},
			},
			Freshness: FreshnessPanel{
				Bucket: "class",
				Buckets: []FreshnessClassBucket{
					{FreshnessClass: "monthly", Total: 10, Fresh: 8, Stale: 2},
				},
				Total:      10,
				TotalStale: 2,
			},
			Drift: DriftPanel{
				Since:           "2026-05-17",
				Through:         "2026-05-24",
				Delta:           -1,
				FlippedOutCount: 1,
				FlippedOut: []DriftRow{
					{ControlID: "ctrl-1", LastPassing: "2026-05-20", CurrentResult: "fail"},
				},
			},
			Upcoming: []UpcomingPanelRow{
				{
					DueDate:      "2026-06-01T00:00:00Z",
					Category:     "exception",
					Title:        "Exception expires",
					ResourceType: "exception",
					ResourceID:   "exc-1",
				},
			},
			Activity: []ActivityPanelRow{
				{
					TS:           "2026-05-23T10:00:00Z",
					EventType:    "evidence.ingest",
					Actor:        "cred-1",
					ResourceType: "evidence",
					ResourceID:   "rec-1",
					Summary:      `{"decision":"accepted"}`,
				},
			},
		},
	}
}

// AC-3: JSON shape carries snapshot_at + panels.{6 keys}.
func TestSlice269_EncodeJSON_ShapeAndKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeJSON(&buf, fixtureSnapshot()); err != nil {
		t.Fatalf("encodeJSON: %v", err)
	}
	s := buf.String()
	for _, want := range []string{
		`"snapshot_at"`,
		`"panels"`,
		`"framework_posture"`,
		`"risks"`,
		`"freshness"`,
		`"drift"`,
		`"upcoming"`,
		`"activity"`,
		`"Customer data leak"`,
		`"monthly"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("encodeJSON output missing %q\nfull body:\n%s", want, s)
		}
	}

	// Re-parse to confirm the body is valid JSON (defends against
	// future encoder refactors).
	var anyMap map[string]any
	if err := json.Unmarshal(buf.Bytes(), &anyMap); err != nil {
		t.Fatalf("json.Unmarshal: %v\nbody:\n%s", err, s)
	}
	if _, ok := anyMap["panels"]; !ok {
		t.Errorf("decoded payload missing top-level 'panels' key")
	}
}

// AC-4: CSV format is a zip with one file per panel.
func TestSlice269_EncodeCSVZip_OneFilePerPanel(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeCSVZip(&buf, fixtureSnapshot()); err != nil {
		t.Fatalf("encodeCSVZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	wantFiles := map[string]bool{
		"framework-posture.csv": false,
		"risks.csv":             false,
		"freshness.csv":         false,
		"drift.csv":             false,
		"upcoming.csv":          false,
		"activity.csv":          false,
	}
	for _, f := range zr.File {
		if _, ok := wantFiles[f.Name]; !ok {
			t.Errorf("unexpected zip member: %q", f.Name)
			continue
		}
		wantFiles[f.Name] = true
	}
	for name, seen := range wantFiles {
		if !seen {
			t.Errorf("missing zip member: %q", name)
		}
	}
}

// AC-4: each CSV body carries its panel's header + at least one row
// (or the freshness sentinel summary row).
func TestSlice269_EncodeCSVZip_PanelBodyShape(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeCSVZip(&buf, fixtureSnapshot()); err != nil {
		t.Fatalf("encodeCSVZip: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))

	// Risks: header + one row containing the fixture title.
	body := zipMemberBody(t, zr, "risks.csv")
	if !strings.HasPrefix(body, "id,title,treatment,category,methodology,residual_score,created_at\r\n") {
		t.Errorf("risks header mismatch; got:\n%s", body)
	}
	if !strings.Contains(body, "Customer data leak") {
		t.Errorf("risks body missing fixture title; got:\n%s", body)
	}

	// Freshness: summary __total__ row first, then class rows.
	fbody := zipMemberBody(t, zr, "freshness.csv")
	if !strings.Contains(fbody, "__total__") {
		t.Errorf("freshness body missing __total__ summary row; got:\n%s", fbody)
	}
	if !strings.Contains(fbody, "monthly") {
		t.Errorf("freshness body missing class row 'monthly'; got:\n%s", fbody)
	}
}

// AC-5: XLSX is a workbook with one sheet per panel.
func TestSlice269_EncodeXLSX_OneSheetPerPanel(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeXLSX(&buf, fixtureSnapshot()); err != nil {
		t.Fatalf("encodeXLSX: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	// Required workbook envelope.
	wantBase := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
	}
	for _, want := range wantBase {
		if !hasZipMember(zr, want) {
			t.Errorf("xlsx missing envelope member %q", want)
		}
	}
	// Six per-panel sheets.
	for i := 1; i <= 6; i++ {
		path := "xl/worksheets/sheet" + strconv.Itoa(i) + ".xml"
		if !hasZipMember(zr, path) {
			t.Errorf("xlsx missing sheet %q", path)
		}
	}

	// workbook.xml must reference all six panel keys as sheet names.
	wb := zipMemberBody(t, zr, "xl/workbook.xml")
	for _, name := range panelOrder {
		if !strings.Contains(wb, `name="`+name+`"`) {
			t.Errorf("workbook.xml missing sheet name=%q; got:\n%s", name, wb)
		}
	}
}

// AC-2 / P0-A5: encodeSnapshot rejects an unknown format.
func TestSlice269_EncodeSnapshot_UnknownFormatErrs(t *testing.T) {
	err := encodeSnapshot(io.Discard, Format("pdf"), fixtureSnapshot())
	if err == nil {
		t.Fatal("encodeSnapshot(pdf) err = nil; want error")
	}
}

// CSV cell sanitizer applies the OWASP leading-rune prefix.
func TestSlice269_SanitizeCSVCell_OWASP(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"", ""},
		{"hello", "hello"},
		{"=SUM(A1)", "'=SUM(A1)"},
		{"+CMD()", "'+CMD()"},
		{"-MID()", "'-MID()"},
		{"@import", "'@import"},
		{"\tboom", "'\tboom"},
		{"\rboom", "'\rboom"},
	}
	for _, tc := range cases {
		got := sanitizeCSVCell(tc.in)
		if got != tc.out {
			t.Errorf("sanitizeCSVCell(%q) = %q; want %q", tc.in, got, tc.out)
		}
	}
}

// CSV quoter applies RFC 4180 quoting to cells that contain
// commas / quotes / CR / LF.
func TestSlice269_CSVQuoteCell_RFC4180(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"plain", "plain"},
		{"has,comma", `"has,comma"`},
		{`has"quote`, `"has""quote"`},
		{"has\nLF", "\"has\nLF\""},
		{"has\rCR", "\"has\rCR\""},
	}
	for _, tc := range cases {
		got := csvQuoteCell(tc.in)
		if got != tc.out {
			t.Errorf("csvQuoteCell(%q) = %q; want %q", tc.in, got, tc.out)
		}
	}
}

// xmlEscapeAttr escapes the five XML attribute metacharacters.
func TestSlice269_XMLEscapeAttr_Metachars(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"plain", "plain"},
		{`a&b`, "a&amp;b"},
		{`a<b`, "a&lt;b"},
		{`a>b`, "a&gt;b"},
		{`a"b`, "a&quot;b"},
	}
	for _, tc := range cases {
		got := xmlEscapeAttr(tc.in)
		if got != tc.out {
			t.Errorf("xmlEscapeAttr(%q) = %q; want %q", tc.in, got, tc.out)
		}
	}
}

// colLetters maps 0->A, 25->Z, 26->AA, 51->AZ, 52->BA.
func TestSlice269_ColLetters_A1Notation(t *testing.T) {
	cases := []struct {
		in  int
		out string
	}{
		{0, "A"},
		{1, "B"},
		{25, "Z"},
		{26, "AA"},
		{27, "AB"},
		{51, "AZ"},
		{52, "BA"},
	}
	for _, tc := range cases {
		got := colLetters(tc.in)
		if got != tc.out {
			t.Errorf("colLetters(%d) = %q; want %q", tc.in, got, tc.out)
		}
	}
}

// xlsxSheetName truncates to 31 chars.
func TestSlice269_XLSXSheetName_TruncatesTo31(t *testing.T) {
	long := strings.Repeat("a", 40)
	got := xlsxSheetName(long)
	if len(got) != 31 {
		t.Errorf("xlsxSheetName length = %d; want 31", len(got))
	}
}

// csvMemberName underscores -> dashes.
func TestSlice269_CSVMemberName_DashesNotUnderscores(t *testing.T) {
	cases := map[string]string{
		"framework_posture": "framework-posture.csv",
		"risks":             "risks.csv",
		"upcoming":          "upcoming.csv",
	}
	for in, want := range cases {
		got := csvMemberName(in)
		if got != want {
			t.Errorf("csvMemberName(%q) = %q; want %q", in, got, want)
		}
	}
}

// ===== panelTable =====

func TestSlice269_PanelTable_PerPanelShape(t *testing.T) {
	s := fixtureSnapshot()
	cases := []struct {
		panel    string
		wantCols int
		minRows  int
	}{
		{"framework_posture", 5, 1},
		{"risks", 7, 1},
		{"freshness", 4, 2}, // 1 summary + 1 class row
		{"drift", 3, 2},     // 1 window summary + 1 flipped-out row
		{"upcoming", 5, 1},
		{"activity", 6, 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.panel, func(t *testing.T) {
			header, rows := panelTable(tc.panel, s)
			if len(header) != tc.wantCols {
				t.Errorf("%s header cols = %d; want %d", tc.panel, len(header), tc.wantCols)
			}
			if len(rows) < tc.minRows {
				t.Errorf("%s row count = %d; want >= %d", tc.panel, len(rows), tc.minRows)
			}
			for i, r := range rows {
				if len(r) != len(header) {
					t.Errorf("%s row %d cell count = %d; want %d (header width)",
						tc.panel, i, len(r), len(header))
				}
			}
		})
	}
}

// Unknown panel name yields a 1-cell single-row table (defensive).
func TestSlice269_PanelTable_UnknownPanel(t *testing.T) {
	header, rows := panelTable("not_a_panel", fixtureSnapshot())
	if len(header) != 1 || header[0] != "panel" {
		t.Errorf("unknown panel header = %v; want [panel]", header)
	}
	if len(rows) != 1 {
		t.Errorf("unknown panel rows = %d; want 1", len(rows))
	}
}

// ===== panelRowCount =====

func TestSlice269_PanelRowCount_SixKeys(t *testing.T) {
	got := panelRowCount(fixtureSnapshot())
	want := []string{
		"framework_posture", "risks", "freshness",
		"drift", "upcoming", "activity",
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("panelRowCount missing key %q", k)
		}
	}
	if got["risks"] != 1 {
		t.Errorf("risks count = %d; want 1", got["risks"])
	}
}

// ===== Handler early-exit branches (no DB needed) =====

// 401: no credential on the context.
func TestSlice269_ExportDashboard_NoCredentialReturns401(t *testing.T) {
	h := NewHandler(nil, &stubSource{})
	req := httptest.NewRequest("GET", "/v1/dashboard/export", nil)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %q; want substring %q",
			rec.Body.String(), "missing credential")
	}
}

// 500: credential present but tenant id is not a UUID.
func TestSlice269_ExportDashboard_InvalidTenantIDReturns500(t *testing.T) {
	h := NewHandler(nil, &stubSource{})
	req := httptest.NewRequest("GET", "/v1/dashboard/export", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test",
		TenantID: "not-a-uuid",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

// 400: unknown format query parameter.
func TestSlice269_ExportDashboard_BadFormatReturns400(t *testing.T) {
	h := NewHandler(nil, &stubSource{})
	req := httptest.NewRequest("GET",
		"/v1/dashboard/export?format=pdf", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test",
		TenantID: "00000000-0000-0000-0000-000000000001",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unsupported format") {
		t.Errorf("body = %q; want substring %q",
			rec.Body.String(), "unsupported format")
	}
}

// 403: caller lacks both IsAdmin and IsApprover.
func TestSlice269_ExportDashboard_ForbiddenReturns403(t *testing.T) {
	h := NewHandler(nil, &stubSource{})
	req := httptest.NewRequest("GET",
		"/v1/dashboard/export?format=json", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:         "test",
		TenantID:   "00000000-0000-0000-0000-000000000001",
		OwnerRoles: []string{"platform-eng"}, // control-owner only
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
}

// 500: source returns an error.
func TestSlice269_ExportDashboard_SourceErrReturns500(t *testing.T) {
	h := NewHandler(nil, &stubSource{err: errors.New("stub: panel boom")})
	req := httptest.NewRequest("GET",
		"/v1/dashboard/export?format=json", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test",
		TenantID: "00000000-0000-0000-0000-000000000001",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
	// Slice 367: the 5xx body is now the generic
	// {"error":"internal error","request_id":"<uuid>"} shape. The
	// op-label "compose dashboard snapshot" lives in the slog log line,
	// not the client body. Assert the generic shape + that the original
	// "panel boom" detail is NOT leaked.
	body := rec.Body.String()
	if !strings.Contains(body, `"error":"internal error"`) {
		t.Errorf("body = %q; want generic internal-error shape", body)
	}
	if !strings.Contains(body, `"request_id":`) {
		t.Errorf("body = %q; want request_id field", body)
	}
	if strings.Contains(body, "panel boom") {
		t.Errorf("slice 367 regression: body leaked raw err: %q", body)
	}
}

// 200: full happy-path with admin + stub source. Body is a valid
// JSON document carrying the six panel keys.
func TestSlice269_ExportDashboard_HappyPath200JSON(t *testing.T) {
	h := NewHandler(nil, &stubSource{snap: fixtureSnapshot()})
	req := httptest.NewRequest("GET",
		"/v1/dashboard/export?format=json", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test",
		TenantID: "00000000-0000-0000-0000-000000000001",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "dashboard_") {
		t.Errorf("Content-Disposition = %q; want substring %q", got, "dashboard_")
	}
	for _, want := range []string{
		`"framework_posture"`,
		`"risks"`,
		`"freshness"`,
		`"drift"`,
		`"upcoming"`,
		`"activity"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q; full:\n%s", want, rec.Body.String())
		}
	}
}

// CSV happy-path produces a zip body.
func TestSlice269_ExportDashboard_HappyPath200CSVZip(t *testing.T) {
	h := NewHandler(nil, &stubSource{snap: fixtureSnapshot()})
	req := httptest.NewRequest("GET",
		"/v1/dashboard/export?format=csv", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test",
		TenantID: "00000000-0000-0000-0000-000000000001",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q; want application/zip", got)
	}
	// Verify zip body parses + contains a CSV file.
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if !hasZipMember(zr, "risks.csv") {
		t.Errorf("zip missing risks.csv member")
	}
}

// XLSX happy-path produces an xlsx body.
func TestSlice269_ExportDashboard_HappyPath200XLSX(t *testing.T) {
	h := NewHandler(nil, &stubSource{snap: fixtureSnapshot()})
	req := httptest.NewRequest("GET",
		"/v1/dashboard/export?format=xlsx", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test",
		TenantID: "00000000-0000-0000-0000-000000000001",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "spreadsheetml.sheet") {
		t.Errorf("Content-Type = %q; want substring %q", got, "spreadsheetml.sheet")
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if !hasZipMember(zr, "xl/workbook.xml") {
		t.Errorf("xlsx missing xl/workbook.xml")
	}
}

// ===== WithSource chains =====

func TestSlice269_WithSource_ReturnsSelf(t *testing.T) {
	h := NewHandler(nil, &stubSource{})
	src := &stubSource{}
	got := h.WithSource(src)
	if got != h {
		t.Errorf("WithSource returned different handler; want self-chain")
	}
	if h.source != src {
		t.Errorf("WithSource did not install the source")
	}
}

// ===== Helpers =====

type stubSource struct {
	snap Snapshot
	err  error
}

func (s *stubSource) Snapshot(_ context.Context) (Snapshot, error) {
	return s.snap, s.err
}

func hasZipMember(zr *zip.Reader, name string) bool {
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

func zipMemberBody(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	t.Fatalf("zip missing %s", name)
	return ""
}
