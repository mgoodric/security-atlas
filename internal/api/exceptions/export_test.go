// Slice 138 — unit tests for the exceptions-export projection helpers
// + the handler dispatch's early-exit branches.

package exceptions

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

// Slice doc says exceptions export INCLUDES owner + duration +
// justification. Lock those columns in here.
func TestSlice138_ExceptionsExportHeader_IncludesOwnerDurationJustification(t *testing.T) {
	got := exceptionsExportHeader()
	want := []string{"justification", "requested_by", "duration_days"}
	for _, col := range want {
		found := false
		for _, h := range got {
			if h == col {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exceptions export header missing required column %q", col)
		}
	}
}

func TestSlice138_ExceptionsToRowIter(t *testing.T) {
	header := exceptionsExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	ctrl := uuid.New()
	expiresAt := now.Add(90 * 24 * time.Hour)
	row := exceptionExportRow{
		ID:                   id,
		ControlID:            ctrl,
		Status:               "approved",
		Justification:        "Compensating control in place via SRE manual review",
		CompensatingControls: "manual-review|alert-on-fail",
		ScopeCellPredicate:   `{"op":"true"}`,
		RequestedBy:          "requester@example.com",
		RequestedAt:          now,
		ApprovedBy:           "approver@example.com",
		ApprovedAt:           now.Format(time.RFC3339),
		ExpiresAt:            expiresAt,
		DurationDays:         90,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	it := exceptionsToRowIter([]exceptionExportRow{row})
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
	if cells[idx("status")] != "approved" {
		t.Errorf("status = %q", cells[idx("status")])
	}
	if cells[idx("duration_days")] != "90" {
		t.Errorf("duration_days = %q; want 90", cells[idx("duration_days")])
	}
	if !strings.Contains(cells[idx("justification")], "Compensating control") {
		t.Errorf("justification body missing")
	}
	if cells[idx("compensating_controls")] != "manual-review|alert-on-fail" {
		t.Errorf("compensating_controls join wrong: %q", cells[idx("compensating_controls")])
	}
}

func TestSlice138_ParseExceptionsExportFormat(t *testing.T) {
	cases := []struct {
		query     string
		want      export.Format
		expectErr bool
	}{
		{"", export.FormatCSV, false},
		{"format=json", export.FormatJSON, false},
		{"format=xlsx", export.FormatXLSX, false},
		{"format=yaml", "yaml", true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/admin/exceptions/export?"+tc.query, nil)
			got, err := parseExceptionsExportFormat(req)
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

func TestSlice138_ExceptionsHasProgramRead(t *testing.T) {
	if exceptionsHasProgramRead(credstore.Credential{}) {
		t.Errorf("bare credential should be denied")
	}
	if !exceptionsHasProgramRead(credstore.Credential{IsAdmin: true}) {
		t.Errorf("admin should be allowed")
	}
}

func TestSlice138_ExceptionsMetaAuditActionConstant(t *testing.T) {
	if metaAuditActionExceptionsExport != "exceptions_export" {
		t.Errorf("metaAuditActionExceptionsExport = %q; want %q",
			metaAuditActionExceptionsExport, "exceptions_export")
	}
}

type stubExceptionsSource struct {
	rows     []exceptionExportRow
	exceeded bool
	err      error
	calls    int
}

func (s *stubExceptionsSource) listForExport(_ context.Context, _ int) ([]exceptionExportRow, bool, error) {
	s.calls++
	return s.rows, s.exceeded, s.err
}

func TestSlice138_ExceptionsExportHandler_BuilderChain(t *testing.T) {
	h := NewExportHandler(nil)
	if h == nil {
		t.Fatal("NewExportHandler returned nil")
	}
	src := &stubExceptionsSource{}
	if got := h.WithSource(src); got != h {
		t.Errorf("WithSource did not chain")
	}
	lim := export.NewLimiter(5)
	if got := h.WithLimiter(lim); got != h {
		t.Errorf("WithLimiter did not chain")
	}
	if h.exportLimiter() != lim {
		t.Errorf("exportLimiter override not honored")
	}
}

func TestSlice138_ExceptionsListForExport_StubDispatch(t *testing.T) {
	want := []exceptionExportRow{{ID: uuid.New(), ControlID: uuid.New(), Status: "approved"}}
	src := &stubExceptionsSource{rows: want}
	h := NewExportHandler(nil).WithSource(src)
	got, _, err := h.listExceptionsForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("rows = %d; want 1", len(got))
	}
	if src.calls != 1 {
		t.Errorf("calls = %d; want 1", src.calls)
	}
}

func TestSlice138_ExceptionsListForExport_StubError(t *testing.T) {
	want := errors.New("e")
	src := &stubExceptionsSource{err: want}
	h := NewExportHandler(nil).WithSource(src)
	if _, _, err := h.listExceptionsForExport(context.Background(), 100); !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
}

func TestSlice138_ExceptionsExportControls_NoCredentialReturns401(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/exceptions/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportExceptions(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
}

func TestSlice138_ExceptionsExportControls_InvalidTenantReturns500(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/exceptions/export?format=csv", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID: "test", TenantID: "not-a-uuid", IsAdmin: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportExceptions(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

func TestSlice138_ExceptionsCountingWriter(t *testing.T) {
	cw := &exceptionsCountingWriter{w: io.Discard}
	if _, err := cw.Write([]byte("12345")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw.n != 5 {
		t.Errorf("n = %d; want 5", cw.n)
	}
}
