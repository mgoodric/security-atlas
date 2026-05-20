// Slice 137 — unit tests for the controls-export projection helpers
// + the handler dispatch's early-exit branches.
//
// The integration suite (`export_integration_test.go`, build-tag
// `integration`) exercises the full wire surface against Postgres
// + RLS; this file covers the pure functions that need no DB plus
// the early-exit handler branches (no-credential, invalid-tenant-id,
// stub-source dispatch through listControlsForExport) that can be
// reached without touching the pgxpool.
//
// Coverage posture: `internal/api/controls/` is not in the CI
// integration-test list (`.github/workflows/ci.yml` line 289–310),
// so unit coverage is the load-bearing measure for this package.
// The per-package floor is 26% (`cmd/scripts/coverage-thresholds.json`);
// the integration-only branches of `ExportControls` (which touch a
// real pgxpool) are validated by `export_integration_test.go`.

package controls

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

// controlsToRowIter must emit rows in canonical column order. Guards
// against a future contributor reshuffling controlsExportHeader
// without updating controlsToRowIter (or vice versa).
func TestSlice137_ControlsToRowIter_ColumnOrderMatchesHeader(t *testing.T) {
	header := controlsExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	anchor := uuid.New()

	row := controlExportRow{
		ID:                 id,
		BundleID:           "bundle-aws-iam",
		Version:            3,
		SCFID:              "IAC-06",
		SCFAnchorID:        anchor,
		Title:              "AWS IAM least-privilege",
		ControlFamily:      "identity-access-management",
		ImplementationType: "automated",
		OwnerRole:          "platform-eng",
		LifecycleState:     "active",
		ApplicabilityExpr:  "BU=eng AND env=prod",
		FreshnessClass:     "fresh",
		BundleManifestHash: "sha256:abcdef0123456789",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	it := controlsToRowIter([]controlExportRow{row})
	var cells []string
	for r := range it {
		cells = r
		break
	}
	if len(cells) != len(header) {
		t.Fatalf("row cell count = %d; want %d (header)", len(cells), len(header))
	}

	// Spot-check the cells at known positions.
	checks := map[string]string{
		"id":                   id.String(),
		"bundle_id":            "bundle-aws-iam",
		"version":              "3",
		"title":                "AWS IAM least-privilege",
		"control_family":       "identity-access-management",
		"scf_id":               "IAC-06",
		"scf_anchor_id":        anchor.String(),
		"implementation_type":  "automated",
		"owner_role":           "platform-eng",
		"lifecycle_state":      "active",
		"applicability_expr":   "BU=eng AND env=prod",
		"freshness_class":      "fresh",
		"bundle_manifest_hash": "sha256:abcdef0123456789",
	}
	for col, want := range checks {
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
		if cells[idx] != want {
			t.Errorf("column %q = %q; want %q", col, cells[idx], want)
		}
	}

	// Timestamp columns render as RFC3339.
	createdIdx := -1
	updatedIdx := -1
	for i, h := range header {
		switch h {
		case "created_at":
			createdIdx = i
		case "updated_at":
			updatedIdx = i
		}
	}
	if !strings.Contains(cells[createdIdx], "T") {
		t.Errorf("created_at = %q; want RFC3339-shaped", cells[createdIdx])
	}
	if !strings.Contains(cells[updatedIdx], "T") {
		t.Errorf("updated_at = %q; want RFC3339-shaped", cells[updatedIdx])
	}
}

// Header positions are stable. Lock the canonical order so a
// downstream consumer keying off a column position cannot be silently
// broken by a header reorder.
func TestSlice137_ControlsExportHeader_StableOrder(t *testing.T) {
	want := []string{
		"id",
		"bundle_id",
		"version",
		"title",
		"control_family",
		"scf_id",
		"scf_anchor_id",
		"implementation_type",
		"owner_role",
		"lifecycle_state",
		"applicability_expr",
		"freshness_class",
		"bundle_manifest_hash",
		"created_at",
		"updated_at",
	}
	got := controlsExportHeader()
	if len(got) != len(want) {
		t.Fatalf("column count = %d; want %d", len(got), len(want))
	}
	for i, c := range want {
		if got[i] != c {
			t.Errorf("column[%d] = %q; want %q", i, got[i], c)
		}
	}
}

// parseControlsExportFormat resolves the query string. Default is CSV;
// unknown values 400; the three valid formats round-trip.
func TestSlice137_ParseControlsExportFormat(t *testing.T) {
	cases := []struct {
		query     string
		want      export.Format
		expectErr bool
	}{
		{"", export.FormatCSV, false},
		{"format=csv", export.FormatCSV, false},
		{"format=json", export.FormatJSON, false},
		{"format=xlsx", export.FormatXLSX, false},
		{"format=CSV", export.FormatCSV, false},   // case-insensitive
		{"format=JSON", export.FormatJSON, false}, // case-insensitive
		{"format=pdf", "pdf", true},               // unsupported
		{"format=html", "html", true},             // unsupported
		{"format=", export.FormatCSV, false},      // empty == default
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/controls/export?"+tc.query, nil)
			got, err := parseControlsExportFormat(req)
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

// Role-gate parity: the controlsHasProgramRead predicate must match
// the slice 067 risk read endpoints' helper. A bare push credential
// (no flags) does NOT carry program-read access; admin / approver /
// owner-roles do.
func TestSlice137_ControlsHasProgramRead(t *testing.T) {
	cases := []struct {
		name string
		c    credstore.Credential
		want bool
	}{
		{"bare", credstore.Credential{}, false},
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"owner", credstore.Credential{OwnerRoles: []string{"control-owner"}}, true},
		{"admin+owner", credstore.Credential{IsAdmin: true, OwnerRoles: []string{"x"}}, true},
		{"empty owner roles", credstore.Credential{OwnerRoles: []string{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := controlsHasProgramRead(tc.c)
			if got != tc.want {
				t.Errorf("controlsHasProgramRead = %v; want %v", got, tc.want)
			}
		})
	}
}

// The meta-audit action constant is the slice 137 D6 plural value.
// Locks the spelling: a contributor who accidentally types
// `control_export` (singular) would surface this test failure rather
// than a CI round-trip failure (which slice 136 cost three times).
func TestSlice137_MetaAuditActionConstant(t *testing.T) {
	if metaAuditActionControlsExport != "controls_export" {
		t.Errorf("metaAuditActionControlsExport = %q; want %q (slice 137 D6 plural convention)",
			metaAuditActionControlsExport, "controls_export")
	}
}

// Default row cap is 500K (slice 137 D3 / P0-A-UCF-1). Locks the
// constant against drift.
func TestSlice137_DefaultRowCap(t *testing.T) {
	if defaultControlsExportRowCap != 500_000 {
		t.Errorf("defaultControlsExportRowCap = %d; want 500000 (slice 137 D3 lifted cap)",
			defaultControlsExportRowCap)
	}
}

// ===== Constructor + builder surface =====
//
// The slice 137 handler exposes a small builder chain (NewExportHandler
// → WithSource → WithLimiter) that the integration suite leans on for
// dependency injection. The unit suite exercises the wiring itself —
// confirming each builder method returns the same handler with the
// override applied, and that exportLimiter() falls back to the
// process-wide singleton when no override is set.

// stubSource is a deterministic implementation of controlsExportSource
// the unit tests use to validate the source-dispatch path in
// listControlsForExport without standing up a pgxpool.
type stubSource struct {
	rows     []controlExportRow
	exceeded bool
	err      error
	calls    int
}

func (s *stubSource) listForExport(_ context.Context, _ int) ([]controlExportRow, bool, error) {
	s.calls++
	return s.rows, s.exceeded, s.err
}

// NewExportHandler returns a non-nil handler with the pool stored on
// it; builder methods chain off the same value.
func TestSlice137_NewExportHandler_StoresPool(t *testing.T) {
	// The exact pool isn't used in this test (we only inspect the
	// returned handler's identity). Passing nil is safe because the
	// constructor never dereferences the pool — only later DB calls
	// would.
	h := NewExportHandler(nil)
	if h == nil {
		t.Fatal("NewExportHandler returned nil")
	}
	if h.source != nil {
		t.Errorf("source default = %v; want nil (production path uses inline adapter)", h.source)
	}
	if h.limiter != nil {
		t.Errorf("limiter default = %v; want nil (production path resolves DefaultLimiter)", h.limiter)
	}
}

// WithSource installs a test source and returns the same handler.
func TestSlice137_WithSource_Chains(t *testing.T) {
	h := NewExportHandler(nil)
	src := &stubSource{}
	got := h.WithSource(src)
	if got != h {
		t.Errorf("WithSource returned a different handler; want self-chain")
	}
	if h.source == nil {
		t.Errorf("WithSource did not install the source on the handler")
	}
}

// WithLimiter installs a test limiter and returns the same handler.
func TestSlice137_WithLimiter_Chains(t *testing.T) {
	h := NewExportHandler(nil)
	lim := export.NewLimiter(3)
	got := h.WithLimiter(lim)
	if got != h {
		t.Errorf("WithLimiter returned a different handler; want self-chain")
	}
	if h.limiter != lim {
		t.Errorf("WithLimiter did not install the limiter on the handler")
	}
}

// exportLimiter falls back to export.DefaultLimiter() when no
// override is set. The override path returns the installed limiter
// verbatim.
func TestSlice137_ExportLimiter_FallbackAndOverride(t *testing.T) {
	t.Run("fallback_to_default", func(t *testing.T) {
		h := NewExportHandler(nil)
		got := h.exportLimiter()
		if got == nil {
			t.Fatal("exportLimiter() returned nil; want DefaultLimiter()")
		}
		// Identity is exactly export.DefaultLimiter() — same process-wide singleton.
		if got != export.DefaultLimiter() {
			t.Errorf("exportLimiter() did not return DefaultLimiter()")
		}
	})
	t.Run("override_returned_verbatim", func(t *testing.T) {
		h := NewExportHandler(nil)
		lim := export.NewLimiter(7)
		h = h.WithLimiter(lim)
		got := h.exportLimiter()
		if got != lim {
			t.Errorf("exportLimiter() = %p; want override %p", got, lim)
		}
	})
}

// listControlsForExport dispatches to source.listForExport when source
// is non-nil; the source's return shape flows through verbatim.
func TestSlice137_ListControlsForExport_StubSourceDispatch(t *testing.T) {
	now := time.Now().UTC()
	want := []controlExportRow{
		{
			ID:                 uuid.New(),
			BundleID:           "bundle-stub-1",
			Version:            1,
			SCFID:              "IAC-06",
			SCFAnchorID:        uuid.New(),
			Title:              "stub control 1",
			ControlFamily:      "identity-access-management",
			ImplementationType: "automated",
			OwnerRole:          "platform-eng",
			LifecycleState:     "active",
			ApplicabilityExpr:  "true",
			FreshnessClass:     "fresh",
			BundleManifestHash: "sha256:stub",
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
	src := &stubSource{rows: want, exceeded: false}
	h := NewExportHandler(nil).WithSource(src)

	got, exceeded, err := h.listControlsForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("listControlsForExport: %v", err)
	}
	if exceeded {
		t.Errorf("exceeded = true; want false (stub source returns exceeded=false)")
	}
	if len(got) != len(want) {
		t.Fatalf("row count = %d; want %d", len(got), len(want))
	}
	if got[0].ID != want[0].ID || got[0].BundleID != want[0].BundleID {
		t.Errorf("row mismatch through stub dispatch: got %+v; want %+v", got[0], want[0])
	}
	if src.calls != 1 {
		t.Errorf("stub source call count = %d; want 1", src.calls)
	}
}

// Stub source can return an error — it flows through unchanged.
func TestSlice137_ListControlsForExport_StubSourceError(t *testing.T) {
	wantErr := errors.New("stub: simulated source error")
	src := &stubSource{err: wantErr}
	h := NewExportHandler(nil).WithSource(src)

	rows, exceeded, err := h.listControlsForExport(context.Background(), 100)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wrapped %v", err, wantErr)
	}
	if rows != nil {
		t.Errorf("rows = %v; want nil on error", rows)
	}
	if exceeded {
		t.Errorf("exceeded = true; want false on error")
	}
}

// Stub source can flag row-cap exceeded — the flag flows through.
func TestSlice137_ListControlsForExport_StubSourceExceeded(t *testing.T) {
	src := &stubSource{exceeded: true}
	h := NewExportHandler(nil).WithSource(src)

	_, exceeded, err := h.listControlsForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !exceeded {
		t.Errorf("exceeded = false; want true (stub source signals row-cap exceeded)")
	}
}

// ===== Handler early-exit branches (no DB needed) =====
//
// `ExportControls` exits BEFORE any pool call on two paths:
//
//   1. No credential in context → 401
//   2. Credential's TenantID is not a valid UUID → 500
//
// Both are reachable with a nil pool. Every later branch
// (bad-format, role-gate, concurrency-cap, encoder-resolve,
// listControlsForExport) writes a meta-audit row before returning,
// which touches the pool; those are exercised by
// `export_integration_test.go` against real Postgres.

// 401 path: no credential in context.
func TestSlice137_ExportControls_NoCredentialReturns401(t *testing.T) {
	h := NewExportHandler(nil) // pool not touched on this path
	req := httptest.NewRequest("GET", "/v1/controls/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportControls(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %q; want substring %q", rec.Body.String(), "missing credential")
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", got)
	}
}

// 500 path: credential present but TenantID is not a valid UUID.
// The handler exits BEFORE the pool is touched because the tenant
// parse fails synchronously inside ExportControls (no meta-audit
// write attempted on this path — the tenant id is the meta-audit
// key, and we don't have one).
func TestSlice137_ExportControls_InvalidTenantIDReturns500(t *testing.T) {
	h := NewExportHandler(nil) // pool not touched on this path
	req := httptest.NewRequest("GET", "/v1/controls/export?format=csv", nil)

	// Inject a credential whose TenantID will NOT parse as a UUID.
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test-id",
		TenantID: "not-a-uuid",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ExportControls(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid tenant") {
		t.Errorf("body = %q; want substring %q", rec.Body.String(), "invalid tenant")
	}
}

// ===== Counting writer =====
//
// controlsCountingWriter wraps an io.Writer and counts bytes written.
// Used by ExportControls to record the body byte count for the
// meta-audit row.

func TestSlice137_ControlsCountingWriter_CountsBytes(t *testing.T) {
	cw := &controlsCountingWriter{w: io.Discard}
	chunk1 := []byte("hello,")
	chunk2 := []byte("world!")

	n, err := cw.Write(chunk1)
	if err != nil {
		t.Fatalf("Write(chunk1): %v", err)
	}
	if n != len(chunk1) {
		t.Errorf("n = %d; want %d", n, len(chunk1))
	}

	n, err = cw.Write(chunk2)
	if err != nil {
		t.Fatalf("Write(chunk2): %v", err)
	}
	if n != len(chunk2) {
		t.Errorf("n = %d; want %d", n, len(chunk2))
	}

	wantTotal := int64(len(chunk1) + len(chunk2))
	if cw.n != wantTotal {
		t.Errorf("cw.n = %d; want %d (sum of chunk lengths)", cw.n, wantTotal)
	}
}

// Counting writer surfaces the underlying Writer's error AND records
// the partial byte count returned by the underlying Write call.
func TestSlice137_ControlsCountingWriter_PropagatesError(t *testing.T) {
	cw := &controlsCountingWriter{w: &shortErrWriter{accept: 3}}
	n, err := cw.Write([]byte("hello"))
	if err == nil {
		t.Fatalf("Write: want error from underlying short writer; got nil")
	}
	if n != 3 {
		t.Errorf("n = %d; want 3 (partial write before error)", n)
	}
	if cw.n != 3 {
		t.Errorf("cw.n = %d; want 3 (partial bytes accounted)", cw.n)
	}
}

// shortErrWriter is a test-only io.Writer that accepts `accept` bytes
// then returns an error. Used to drive the counting writer's
// error-propagation branch.
type shortErrWriter struct {
	accept int
}

func (s *shortErrWriter) Write(p []byte) (int, error) {
	if len(p) <= s.accept {
		s.accept -= len(p)
		return len(p), nil
	}
	taken := s.accept
	s.accept = 0
	return taken, errors.New("short writer: simulated truncation")
}
