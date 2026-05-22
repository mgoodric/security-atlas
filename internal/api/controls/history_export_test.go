// Slice 175 — unit tests for the controls history-export projection
// helpers + the handler dispatch's early-exit branches.
//
// The integration suite (`history_export_integration_test.go`,
// build-tag `integration`) exercises the full wire surface against
// Postgres + RLS; this file covers the pure functions that need no DB
// plus the early-exit handler branches that can be reached without
// touching the pgxpool.
//
// Coverage posture: `internal/api/controls/` is not in the CI
// integration-test list, so unit coverage is the load-bearing measure
// for this package (slice 137 D5 / D8 precedent). The per-package
// floor is 26% (`cmd/scripts/coverage-thresholds.json`).

package controls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/export"
)

// Slice 175 P0-A-175-1: the 17-column history-export header MUST be
// the slice 137 15 columns IN THE SAME ORDER followed by the two new
// supersession columns. Locks the canonical shape so a contributor
// who reorders columns surfaces this test failure rather than a
// downstream-consumer regression.
func TestSlice175_HistoryHeader_LockedShape(t *testing.T) {
	got := controlsHistoryExportHeader()

	// First 15 columns MUST equal slice 137's controlsExportHeader().
	slice137 := controlsExportHeader()
	if len(slice137) != 15 {
		t.Fatalf("slice 137 header length = %d; want 15 (slice 137 D2 baseline)", len(slice137))
	}
	if len(got) != 17 {
		t.Fatalf("slice 175 header length = %d; want 17 (slice 175 AC-2)", len(got))
	}
	for i, want := range slice137 {
		if got[i] != want {
			t.Errorf("history header[%d] = %q; want %q (slice 137 column shift)",
				i, got[i], want)
		}
	}
	// Two new columns at positions 15 and 16.
	if got[15] != "superseded_by" {
		t.Errorf("history header[15] = %q; want %q", got[15], "superseded_by")
	}
	if got[16] != "superseded_at" {
		t.Errorf("history header[16] = %q; want %q", got[16], "superseded_at")
	}
}

// controlsHistoryToRowIter must emit rows in canonical column order.
// Active row → empty supersession cells; superseded row → both cells
// populated.
func TestSlice175_HistoryRowIter_ActiveAndSupersededShapes(t *testing.T) {
	now := time.Now().UTC()
	supersededTS := now.Add(-30 * 24 * time.Hour) // 30 days ago
	activeID := uuid.New()
	supersededID := uuid.New()
	successorID := uuid.New()
	anchor := uuid.New()

	rows := []controlHistoryExportRow{
		{
			ID:                 activeID,
			BundleID:           "bundle-aws-iam",
			Version:            2,
			SCFID:              "IAC-06",
			SCFAnchorID:        anchor,
			Title:              "AWS IAM least-privilege",
			ControlFamily:      "identity-access-management",
			ImplementationType: "automated",
			OwnerRole:          "platform-eng",
			LifecycleState:     "active",
			ApplicabilityExpr:  "BU=eng AND env=prod",
			FreshnessClass:     "fresh",
			BundleManifestHash: "sha256:v2hash",
			CreatedAt:          now,
			UpdatedAt:          now,
			// Active: no successor.
			SupersededBy: uuid.Nil,
			SupersededAt: time.Time{},
		},
		{
			ID:                 supersededID,
			BundleID:           "bundle-aws-iam",
			Version:            1,
			SCFID:              "IAC-06",
			SCFAnchorID:        anchor,
			Title:              "AWS IAM least-privilege (v1)",
			ControlFamily:      "identity-access-management",
			ImplementationType: "automated",
			OwnerRole:          "platform-eng",
			LifecycleState:     "active",
			ApplicabilityExpr:  "BU=eng AND env=prod",
			FreshnessClass:     "fresh",
			BundleManifestHash: "sha256:v1hash",
			CreatedAt:          now.Add(-60 * 24 * time.Hour),
			UpdatedAt:          supersededTS,
			// Superseded: successor + synth timestamp.
			SupersededBy: successorID,
			SupersededAt: supersededTS,
		},
	}

	it := controlsHistoryToRowIter(rows)
	collected := make([][]string, 0, len(rows))
	for r := range it {
		// Defensive copy — the iterator MAY reuse the row buffer.
		cp := make([]string, len(r))
		copy(cp, r)
		collected = append(collected, cp)
	}
	if len(collected) != len(rows) {
		t.Fatalf("collected %d rows; want %d", len(collected), len(rows))
	}

	// Active row: index 0; superseded_by + superseded_at empty.
	active := collected[0]
	if len(active) != 17 {
		t.Fatalf("active row cell count = %d; want 17", len(active))
	}
	if active[0] != activeID.String() {
		t.Errorf("active[0] (id) = %q; want %q", active[0], activeID.String())
	}
	if active[15] != "" {
		t.Errorf("active row[15] (superseded_by) = %q; want empty (active row)", active[15])
	}
	if active[16] != "" {
		t.Errorf("active row[16] (superseded_at) = %q; want empty (active row)", active[16])
	}

	// Superseded row: index 1; both cells populated.
	superseded := collected[1]
	if superseded[0] != supersededID.String() {
		t.Errorf("superseded[0] (id) = %q; want %q", superseded[0], supersededID.String())
	}
	if superseded[15] != successorID.String() {
		t.Errorf("superseded row[15] (superseded_by) = %q; want %q",
			superseded[15], successorID.String())
	}
	if !strings.Contains(superseded[16], "T") {
		t.Errorf("superseded row[16] (superseded_at) = %q; want RFC3339-shaped",
			superseded[16])
	}
	// Slice 175 P0-A-175-1 guard: the 15 slice-137 columns retain
	// their positions. Spot-check `applicability_expr` @ 10.
	if superseded[10] != "BU=eng AND env=prod" {
		t.Errorf("superseded row[10] (applicability_expr) = %q; want %q (slice 137 column shift)",
			superseded[10], "BU=eng AND env=prod")
	}
}

// Default row cap is 500K (slice 175 D3 — matches slice 137).
func TestSlice175_DefaultRowCap(t *testing.T) {
	if defaultControlsHistoryExportRowCap != 500_000 {
		t.Errorf("defaultControlsHistoryExportRowCap = %d; want 500000",
			defaultControlsHistoryExportRowCap)
	}
}

// Meta-audit action is `controls_history_export` (slice 175 D5).
// Locks the spelling — the slice-137 D6 precedent cost CI rounds when
// pluralisation drifted; this test catches typos at unit-test time.
func TestSlice175_MetaAuditActionConstant(t *testing.T) {
	if metaAuditActionControlsHistoryExport != "controls_history_export" {
		t.Errorf("metaAuditActionControlsHistoryExport = %q; want %q",
			metaAuditActionControlsHistoryExport, "controls_history_export")
	}
}

// Entity name used by BuildFilename is `controls_history` so downloaded
// filenames look like `controls_history_20260522.csv`.
func TestSlice175_EntityNameForFilename(t *testing.T) {
	if controlsHistoryExportEntity != "controls_history" {
		t.Errorf("controlsHistoryExportEntity = %q; want %q",
			controlsHistoryExportEntity, "controls_history")
	}
}

// ===== Constructor + builder surface =====

// stubHistorySource is a deterministic implementation of
// controlsHistoryExportSource the unit tests use to validate the
// source-dispatch path in listHistoryForExport without standing up a
// pgxpool.
type stubHistorySource struct {
	rows     []controlHistoryExportRow
	exceeded bool
	err      error
	calls    int
}

func (s *stubHistorySource) listHistoryForExport(_ context.Context, _ int) ([]controlHistoryExportRow, bool, error) {
	s.calls++
	return s.rows, s.exceeded, s.err
}

func TestSlice175_NewHistoryExportHandler_StoresPool(t *testing.T) {
	h := NewHistoryExportHandler(nil)
	if h == nil {
		t.Fatal("NewHistoryExportHandler returned nil")
	}
	if h.source != nil {
		t.Errorf("source default = %v; want nil", h.source)
	}
	if h.limiter != nil {
		t.Errorf("limiter default = %v; want nil", h.limiter)
	}
}

func TestSlice175_HistoryWithSource_Chains(t *testing.T) {
	h := NewHistoryExportHandler(nil)
	src := &stubHistorySource{}
	got := h.WithSource(src)
	if got != h {
		t.Errorf("WithSource returned a different handler; want self-chain")
	}
	if h.source == nil {
		t.Errorf("WithSource did not install the source on the handler")
	}
}

func TestSlice175_HistoryWithLimiter_Chains(t *testing.T) {
	h := NewHistoryExportHandler(nil)
	lim := export.NewLimiter(3)
	got := h.WithLimiter(lim)
	if got != h {
		t.Errorf("WithLimiter returned a different handler; want self-chain")
	}
	if h.limiter != lim {
		t.Errorf("WithLimiter did not install the limiter on the handler")
	}
}

func TestSlice175_HistoryExportLimiter_FallbackAndOverride(t *testing.T) {
	t.Run("fallback_to_default", func(t *testing.T) {
		h := NewHistoryExportHandler(nil)
		got := h.exportLimiter()
		if got == nil {
			t.Fatal("exportLimiter() returned nil; want DefaultLimiter()")
		}
		if got != export.DefaultLimiter() {
			t.Errorf("exportLimiter() did not return DefaultLimiter()")
		}
	})
	t.Run("override_returned_verbatim", func(t *testing.T) {
		h := NewHistoryExportHandler(nil)
		lim := export.NewLimiter(7)
		h = h.WithLimiter(lim)
		got := h.exportLimiter()
		if got != lim {
			t.Errorf("exportLimiter() = %p; want override %p", got, lim)
		}
	})
}

func TestSlice175_ListHistoryForExport_StubSourceDispatch(t *testing.T) {
	now := time.Now().UTC()
	want := []controlHistoryExportRow{
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
	src := &stubHistorySource{rows: want, exceeded: false}
	h := NewHistoryExportHandler(nil).WithSource(src)

	got, exceeded, err := h.listHistoryForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("listHistoryForExport: %v", err)
	}
	if exceeded {
		t.Errorf("exceeded = true; want false")
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

func TestSlice175_ListHistoryForExport_StubSourceError(t *testing.T) {
	wantErr := errors.New("stub: simulated source error")
	src := &stubHistorySource{err: wantErr}
	h := NewHistoryExportHandler(nil).WithSource(src)

	rows, exceeded, err := h.listHistoryForExport(context.Background(), 100)
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

func TestSlice175_ListHistoryForExport_StubSourceExceeded(t *testing.T) {
	src := &stubHistorySource{exceeded: true}
	h := NewHistoryExportHandler(nil).WithSource(src)

	_, exceeded, err := h.listHistoryForExport(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !exceeded {
		t.Errorf("exceeded = false; want true")
	}
}

// ===== Handler early-exit branches (no DB needed) =====

func TestSlice175_ExportControlsHistory_NoCredentialReturns401(t *testing.T) {
	h := NewHistoryExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/controls/history/export?format=csv", nil)
	rec := httptest.NewRecorder()
	h.ExportControlsHistory(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %q; want substring %q", rec.Body.String(), "missing credential")
	}
}

func TestSlice175_ExportControlsHistory_InvalidTenantIDReturns500(t *testing.T) {
	h := NewHistoryExportHandler(nil)
	req := httptest.NewRequest("GET", "/v1/controls/history/export?format=csv", nil)

	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test-id",
		TenantID: "not-a-uuid",
		IsAdmin:  true,
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ExportControlsHistory(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid tenant") {
		t.Errorf("body = %q; want substring %q", rec.Body.String(), "invalid tenant")
	}
}

// Empty input row set produces an empty iter.Seq (no panic, zero yields).
// Important branch: the integration `empty-tenant` path drives this
// through the encoder, but the iterator-level branch belongs in the
// unit suite for fast feedback.
func TestSlice175_HistoryRowIter_EmptyInputYieldsNothing(t *testing.T) {
	it := controlsHistoryToRowIter(nil)
	count := 0
	for range it {
		count++
	}
	if count != 0 {
		t.Errorf("yielded %d rows over nil input; want 0", count)
	}

	it = controlsHistoryToRowIter([]controlHistoryExportRow{})
	count = 0
	for range it {
		count++
	}
	if count != 0 {
		t.Errorf("yielded %d rows over empty slice; want 0", count)
	}
}

// controlsHistoryToRowIter honours early termination — if the consumer
// returns false from yield, the iterator stops. Validates the iter.Seq
// contract the encoder relies on (it stops mid-stream when the writer
// errors).
func TestSlice175_HistoryRowIter_EarlyTermination(t *testing.T) {
	rows := make([]controlHistoryExportRow, 5)
	for i := range rows {
		rows[i] = controlHistoryExportRow{
			ID:                 uuid.New(),
			BundleID:           fmt.Sprintf("bundle-%d", i),
			Version:            int32(i + 1),
			SCFAnchorID:        uuid.New(),
			Title:              "x",
			ControlFamily:      "x",
			ImplementationType: "automated",
			OwnerRole:          "x",
			LifecycleState:     "active",
			ApplicabilityExpr:  "true",
			BundleManifestHash: "x",
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}
	}

	it := controlsHistoryToRowIter(rows)
	seen := 0
	for range it {
		seen++
		if seen == 2 {
			// Break out of the loop — exercises the iter.Seq's
			// early-termination branch (`if !yield(row) return`).
			break
		}
	}
	if seen != 2 {
		t.Errorf("seen = %d; want 2 (early termination)", seen)
	}
}

// The meta-audit struct's JSON tags must serialise the result + format
// fields. This is a wire-format lock: a downstream forensic query that
// filters on `result` or `format` would break silently if a contributor
// renamed the JSON tags.
func TestSlice175_MetaAuditJSONShape(t *testing.T) {
	m := controlsHistoryExportMetaAudit{
		Format:    "csv",
		Result:    "success",
		Reason:    "x",
		RowCount:  42,
		ByteCount: 1234,
	}
	blob, err := jsonMarshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(blob)
	for _, want := range []string{
		`"format":"csv"`,
		`"result":"success"`,
		`"reason":"x"`,
		`"row_count":42`,
		`"byte_count":1234`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("marshal missing %q; got %s", want, s)
		}
	}

	// Empty reason is omitted (omitempty).
	m.Reason = ""
	blob, _ = jsonMarshal(m)
	if strings.Contains(string(blob), `"reason"`) {
		t.Errorf("empty reason should be omitted; got %s", string(blob))
	}
}

// jsonMarshal is the test-local marshal helper; not part of the
// handler's public surface. Kept as a closure so the test file can
// import encoding/json without polluting the handler file's imports.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
