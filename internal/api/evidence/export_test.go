// Slice 138 — unit tests for the evidence-export projection helpers
// + the handler dispatch's early-exit branches.
//
// Pattern mirrors slice 137's controls/export_test.go: unit tests
// cover pure helpers (header, parseFormat, toRowIter, role-gate, the
// constants, the limiter accessor, the counting writer) plus the
// handler early-exit branches that don't touch the pool (no-credential,
// invalid-tenant-id). Full-wire behaviour (cap-exceeded, success
// streaming, meta-audit row, cross-tenant isolation) is validated in
// the package's existing integration test surface when added.

package evidence

import (
	"context"
	"errors"
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

// evidenceExportHeader is the canonical column list. The slice 138
// load-bearing decision (P0-A-Ledger-1 / D1) is **payload is absent**.
// This test locks that in: any future contributor who adds "payload"
// or "payload_json" to the header lights up here, BEFORE the migration
// round-trip or a downstream consumer notices the leak.
func TestSlice138_EvidenceExportHeader_ExcludesPayload(t *testing.T) {
	got := evidenceExportHeader()
	for _, col := range got {
		if strings.EqualFold(col, "payload") || strings.EqualFold(col, "payload_json") {
			t.Fatalf("evidence export header contains forbidden column %q "+
				"— slice 138 P0-A-Ledger-1 excludes payload at v1", col)
		}
	}
	// Must contain the required surfaces per slice doc.
	required := map[string]bool{
		"content_hash":    false,
		"observed_at":     false,
		"freshness_class": false,
		"id":              false,
		"control_id":      false,
	}
	for _, col := range got {
		if _, ok := required[col]; ok {
			required[col] = true
		}
	}
	for col, present := range required {
		if !present {
			t.Errorf("evidence export header missing required column %q", col)
		}
	}
}

func TestSlice138_EvidenceToRowIter_ColumnOrderMatchesHeader(t *testing.T) {
	header := evidenceExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	ctrl := uuid.New()
	scope := uuid.New()
	q := uuid.New()
	row := evidenceExportRow{
		ID:              id,
		ControlID:       ctrl,
		ScopeID:         scope.String(),
		EvidenceQueryID: q.String(),
		ObservedAt:      now,
		IngestedAt:      now,
		Result:          "pass",
		FreshnessClass:  "fresh",
		ContentHash:     "sha256:abcdef",
		PayloadURI:      "s3://bucket/artifact/abc",
		ValidUntil:      now.Format(time.RFC3339),
		CreatedAt:       now,
	}
	it := evidenceToRowIter([]evidenceExportRow{row})
	var cells []string
	for r := range it {
		cells = r
		break
	}
	if len(cells) != len(header) {
		t.Fatalf("row cell count = %d; want %d (header)", len(cells), len(header))
	}
	want := map[string]string{
		"id":                id.String(),
		"control_id":        ctrl.String(),
		"scope_id":          scope.String(),
		"evidence_query_id": q.String(),
		"result":            "pass",
		"freshness_class":   "fresh",
		"content_hash":      "sha256:abcdef",
		"payload_uri":       "s3://bucket/artifact/abc",
	}
	for col, expected := range want {
		idx := -1
		for i, h := range header {
			if h == col {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Errorf("column %q missing from header", col)
			continue
		}
		if cells[idx] != expected {
			t.Errorf("column %q = %q; want %q", col, cells[idx], expected)
		}
	}
}

func TestSlice138_ParseEvidenceExportFormat(t *testing.T) {
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
			req := httptest.NewRequest("GET", "/v1/admin/evidence/export?"+tc.query, nil)
			got, err := parseEvidenceExportFormat(req)
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

func TestSlice138_EvidenceHasProgramRead(t *testing.T) {
	cases := []struct {
		name string
		c    credstore.Credential
		want bool
	}{
		{"bare", credstore.Credential{}, false},
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"owner", credstore.Credential{OwnerRoles: []string{"control-owner"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evidenceHasProgramRead(tc.c); got != tc.want {
				t.Errorf("evidenceHasProgramRead = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestSlice138_EvidenceMetaAuditActionConstant(t *testing.T) {
	if metaAuditActionEvidenceExport != "evidence_export" {
		t.Errorf("metaAuditActionEvidenceExport = %q; want %q",
			metaAuditActionEvidenceExport, "evidence_export")
	}
}

// stubEvidenceSource — deterministic source for source-dispatch tests.
type stubEvidenceSource struct {
	rows     []evidenceExportRow
	exceeded bool
	err      error
	calls    int
}

func (s *stubEvidenceSource) listForExport(_ context.Context, _ int) ([]evidenceExportRow, bool, error) {
	s.calls++
	return s.rows, s.exceeded, s.err
}

func TestSlice138_EvidenceExportHandler_BuilderChain(t *testing.T) {
	h := NewExportHandler(nil)
	if h == nil {
		t.Fatal("NewExportHandler returned nil")
	}
	src := &stubEvidenceSource{}
	if got := h.WithSource(src); got != h {
		t.Errorf("WithSource did not chain")
	}
	lim := export.NewLimiter(3)
	if got := h.WithLimiter(lim); got != h {
		t.Errorf("WithLimiter did not chain")
	}
	if h.exportLimiter() != lim {
		t.Errorf("exportLimiter() did not return override")
	}
}

func TestSlice138_EvidenceListForExport_StubDispatch(t *testing.T) {
	want := []evidenceExportRow{
		{ID: uuid.New(), ControlID: uuid.New(), Result: "pass"},
	}
	src := &stubEvidenceSource{rows: want}
	h := NewExportHandler(nil).WithSource(src)
	got, exceeded, err := h.listEvidenceForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("listEvidenceForExport: %v", err)
	}
	if exceeded {
		t.Errorf("exceeded = true; want false")
	}
	if len(got) != 1 || got[0].ID != want[0].ID {
		t.Errorf("source dispatch did not pass through")
	}
	if src.calls != 1 {
		t.Errorf("source call count = %d; want 1", src.calls)
	}
}

func TestSlice138_EvidenceListForExport_StubError(t *testing.T) {
	want := errors.New("stub error")
	src := &stubEvidenceSource{err: want}
	h := NewExportHandler(nil).WithSource(src)
	_, _, err := h.listEvidenceForExport(context.Background(), 100)
	if !errors.Is(err, want) {
		t.Errorf("err = %v; want %v", err, want)
	}
}

func TestSlice138_EvidenceExportControls_NoCredentialReturns401(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/evidence/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportEvidence(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %q; want missing-credential", rec.Body.String())
	}
}

func TestSlice138_EvidenceExportControls_InvalidTenantReturns500(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/evidence/export?format=csv", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID: "test", TenantID: "not-a-uuid", IsAdmin: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportEvidence(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

func TestSlice138_EvidenceCountingWriter(t *testing.T) {
	cw := &evidenceCountingWriter{w: io.Discard}
	if _, err := cw.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw.n != 5 {
		t.Errorf("n = %d; want 5", cw.n)
	}
}
