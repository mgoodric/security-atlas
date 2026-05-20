// Slice 138 — unit tests for the samples-export projection helpers
// + the handler dispatch's early-exit branches.

package audit

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

// Slice doc requires audit_period_id link to be present in the
// samples export. Lock that in.
func TestSlice138_SamplesExportHeader_IncludesAuditPeriodLink(t *testing.T) {
	got := samplesExportHeader()
	required := []string{"audit_period_id", "population_id", "n", "seed", "window_start", "window_end"}
	for _, col := range required {
		found := false
		for _, h := range got {
			if h == col {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("samples export header missing required column %q", col)
		}
	}
}

func TestSlice138_SamplesDefaultRowCap_Is250K(t *testing.T) {
	if defaultSamplesExportRowCap != 250_000 {
		t.Errorf("defaultSamplesExportRowCap = %d; want 250000 (slice 138 D-lock)",
			defaultSamplesExportRowCap)
	}
}

func TestSlice138_SamplesToRowIter(t *testing.T) {
	header := samplesExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	pop := uuid.New()
	ap := uuid.New()
	ctrl := uuid.New()
	row := sampleExportRow{
		ID:                 id,
		PopulationID:       pop,
		AuditPeriodID:      ap.String(),
		ControlID:          ctrl,
		N:                  40,
		Seed:               "audit-2026-q1",
		CreatedBy:          "lead-auditor@example.com",
		CreatedAt:          now,
		WindowStart:        now.Add(-90 * 24 * time.Hour),
		WindowEnd:          now,
		PopulationFrozenAt: now.Format(time.RFC3339),
		PopulationRowCount: 1234,
	}
	it := samplesToRowIter([]sampleExportRow{row})
	var cells []string
	for r := range it {
		cells = r
		break
	}
	if len(cells) != len(header) {
		t.Fatalf("cells = %d; want %d", len(cells), len(header))
	}
	idx := func(col string) int {
		for i, h := range header {
			if h == col {
				return i
			}
		}
		return -1
	}
	if cells[idx("id")] != id.String() {
		t.Errorf("id wrong")
	}
	if cells[idx("audit_period_id")] != ap.String() {
		t.Errorf("audit_period_id = %q; want %q", cells[idx("audit_period_id")], ap.String())
	}
	if cells[idx("n")] != "40" {
		t.Errorf("n = %q; want 40", cells[idx("n")])
	}
	if cells[idx("seed")] != "audit-2026-q1" {
		t.Errorf("seed = %q", cells[idx("seed")])
	}
	if cells[idx("population_row_count")] != "1234" {
		t.Errorf("population_row_count = %q", cells[idx("population_row_count")])
	}
}

func TestSlice138_ParseSamplesExportFormat(t *testing.T) {
	cases := []struct {
		query     string
		want      export.Format
		expectErr bool
	}{
		{"", export.FormatCSV, false},
		{"format=csv", export.FormatCSV, false},
		{"format=json", export.FormatJSON, false},
		{"format=xlsx", export.FormatXLSX, false},
		{"format=html", "html", true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/admin/samples/export?"+tc.query, nil)
			got, err := parseSamplesExportFormat(req)
			if tc.expectErr {
				if err == nil {
					t.Errorf("want error; got %q", got)
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

func TestSlice138_SamplesHasProgramRead(t *testing.T) {
	if samplesHasProgramRead(credstore.Credential{}) {
		t.Errorf("bare should be denied")
	}
	if !samplesHasProgramRead(credstore.Credential{IsAdmin: true}) {
		t.Errorf("admin should be allowed")
	}
	if !samplesHasProgramRead(credstore.Credential{IsApprover: true}) {
		t.Errorf("approver should be allowed")
	}
}

func TestSlice138_SamplesMetaAuditActionConstant(t *testing.T) {
	if metaAuditActionSamplesExport != "samples_export" {
		t.Errorf("metaAuditActionSamplesExport = %q; want %q",
			metaAuditActionSamplesExport, "samples_export")
	}
}

type stubSamplesSource struct {
	rows     []sampleExportRow
	exceeded bool
	err      error
	calls    int
}

func (s *stubSamplesSource) listForExport(_ context.Context, _ int) ([]sampleExportRow, bool, error) {
	s.calls++
	return s.rows, s.exceeded, s.err
}

func TestSlice138_SamplesExportHandler_BuilderChain(t *testing.T) {
	h := NewSamplesExportHandler(nil)
	if h == nil {
		t.Fatal("NewSamplesExportHandler returned nil")
	}
	src := &stubSamplesSource{}
	if got := h.WithSource(src); got != h {
		t.Errorf("WithSource did not chain")
	}
	lim := export.NewLimiter(4)
	if got := h.WithLimiter(lim); got != h {
		t.Errorf("WithLimiter did not chain")
	}
	if h.exportLimiter() != lim {
		t.Errorf("exportLimiter override not honored")
	}
}

func TestSlice138_SamplesListForExport_StubDispatch(t *testing.T) {
	want := []sampleExportRow{{ID: uuid.New(), PopulationID: uuid.New(), N: 25}}
	src := &stubSamplesSource{rows: want}
	h := NewSamplesExportHandler(nil).WithSource(src)
	got, _, err := h.listSamplesForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].N != 25 {
		t.Errorf("dispatch did not pass through")
	}
	if src.calls != 1 {
		t.Errorf("calls = %d; want 1", src.calls)
	}
}

func TestSlice138_SamplesListForExport_StubError(t *testing.T) {
	want := errors.New("e")
	src := &stubSamplesSource{err: want}
	h := NewSamplesExportHandler(nil).WithSource(src)
	if _, _, err := h.listSamplesForExport(context.Background(), 100); !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
}

func TestSlice138_SamplesExportControls_NoCredentialReturns401(t *testing.T) {
	h := NewSamplesExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/samples/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportSamples(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestSlice138_SamplesExportControls_InvalidTenantReturns500(t *testing.T) {
	h := NewSamplesExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/samples/export?format=csv", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID: "test", TenantID: "not-a-uuid", IsAdmin: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportSamples(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

func TestSlice138_SamplesCountingWriter(t *testing.T) {
	cw := &samplesCountingWriter{w: io.Discard}
	if _, err := cw.Write([]byte("abc")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw.n != 3 {
		t.Errorf("n = %d; want 3", cw.n)
	}
}
