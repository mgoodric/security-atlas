// Slice 138 — unit tests for the policies-export projection helpers
// + the handler dispatch's early-exit branches.

package policies

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/export"
)

func TestSlice138_PoliciesExportHeader_StableOrder(t *testing.T) {
	want := []string{
		"id", "title", "version", "status", "effective_date",
		"owner", "approver", "acknowledgment_required_role",
		"next_review_at", "body_md", "created_at", "updated_at",
	}
	got := policiesExportHeader()
	if len(got) != len(want) {
		t.Fatalf("column count = %d; want %d", len(got), len(want))
	}
	for i, c := range want {
		if got[i] != c {
			t.Errorf("column[%d] = %q; want %q", i, got[i], c)
		}
	}
}

func TestSlice138_PoliciesToRowIter(t *testing.T) {
	header := policiesExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	row := policyExportRow{
		ID:                          id,
		Title:                       "Access Control Policy",
		Version:                     3,
		Status:                      "approved",
		EffectiveDate:               "2026-01-01",
		Owner:                       "ciso@example.com",
		Approver:                    "ceo@example.com",
		AcknowledgmentRequiredRoles: "engineer,manager",
		NextReviewAt:                now.Format(time.RFC3339),
		BodyMD:                      "# Policy\n\nbody",
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	it := policiesToRowIter([]policyExportRow{row})
	var cells []string
	for r := range it {
		cells = r
		break
	}
	if len(cells) != len(header) {
		t.Fatalf("cell count = %d; want %d", len(cells), len(header))
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
		t.Errorf("id column wrong: %q", cells[idx("id")])
	}
	if cells[idx("version")] != "3" {
		t.Errorf("version column = %q; want 3", cells[idx("version")])
	}
	if cells[idx("acknowledgment_required_role")] != "engineer,manager" {
		t.Errorf("ack roles = %q", cells[idx("acknowledgment_required_role")])
	}
	if cells[idx("body_md")] != "# Policy\n\nbody" {
		t.Errorf("body_md = %q", cells[idx("body_md")])
	}
}

func TestSlice138_ParsePoliciesExportFormat(t *testing.T) {
	cases := []struct {
		query     string
		want      export.Format
		expectErr bool
	}{
		{"", export.FormatCSV, false},
		{"format=csv", export.FormatCSV, false},
		{"format=json", export.FormatJSON, false},
		{"format=xlsx", export.FormatXLSX, false},
		{"format=pdf", "pdf", true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/admin/policies/export?"+tc.query, nil)
			got, err := parsePoliciesExportFormat(req)
			if tc.expectErr {
				if err == nil {
					t.Errorf("want error; got format=%q", got)
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

func TestSlice138_PoliciesHasProgramRead(t *testing.T) {
	if policiesHasProgramRead(credstore.Credential{}) {
		t.Errorf("bare credential should be denied")
	}
	if !policiesHasProgramRead(credstore.Credential{IsAdmin: true}) {
		t.Errorf("admin should be allowed")
	}
	if !policiesHasProgramRead(credstore.Credential{IsApprover: true}) {
		t.Errorf("approver should be allowed")
	}
	if !policiesHasProgramRead(credstore.Credential{OwnerRoles: []string{"x"}}) {
		t.Errorf("owner should be allowed")
	}
}

func TestSlice138_PoliciesMetaAuditActionConstant(t *testing.T) {
	if metaAuditActionPoliciesExport != "policies_export" {
		t.Errorf("metaAuditActionPoliciesExport = %q; want %q",
			metaAuditActionPoliciesExport, "policies_export")
	}
}

type stubPoliciesSource struct {
	rows     []policyExportRow
	exceeded bool
	err      error
	calls    int
}

func (s *stubPoliciesSource) listForExport(_ context.Context, _ int) ([]policyExportRow, bool, error) {
	s.calls++
	return s.rows, s.exceeded, s.err
}

func TestSlice138_PoliciesExportHandler_BuilderChain(t *testing.T) {
	h := NewExportHandler(nil)
	if h == nil {
		t.Fatal("NewExportHandler returned nil")
	}
	src := &stubPoliciesSource{}
	if got := h.WithSource(src); got != h {
		t.Errorf("WithSource did not chain")
	}
	lim := export.NewLimiter(2)
	if got := h.WithLimiter(lim); got != h {
		t.Errorf("WithLimiter did not chain")
	}
	if h.exportLimiter() != lim {
		t.Errorf("exportLimiter override not honored")
	}
}

func TestSlice138_PoliciesListForExport_StubDispatch(t *testing.T) {
	want := []policyExportRow{{ID: uuid.New(), Title: "X"}}
	src := &stubPoliciesSource{rows: want}
	h := NewExportHandler(nil).WithSource(src)
	got, _, err := h.listPoliciesForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].Title != "X" {
		t.Errorf("dispatch did not pass through")
	}
	if src.calls != 1 {
		t.Errorf("call count = %d; want 1", src.calls)
	}
}

func TestSlice138_PoliciesListForExport_StubError(t *testing.T) {
	want := errors.New("err")
	src := &stubPoliciesSource{err: want}
	h := NewExportHandler(nil).WithSource(src)
	if _, _, err := h.listPoliciesForExport(context.Background(), 100); !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
}

func TestSlice138_PoliciesExportControls_NoCredentialReturns401(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/policies/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportPolicies(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
}

func TestSlice138_PoliciesExportControls_InvalidTenantReturns500(t *testing.T) {
	h := NewExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/admin/policies/export?format=csv", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID: "test", TenantID: "not-a-uuid", IsAdmin: true,
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportPolicies(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

func TestSlice138_PoliciesCountingWriter(t *testing.T) {
	cw := &policiesCountingWriter{w: io.Discard}
	if _, err := cw.Write([]byte("abcdef")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw.n != 6 {
		t.Errorf("n = %d; want 6", cw.n)
	}
}
