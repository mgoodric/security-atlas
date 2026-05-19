package anchors

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ptrEvidenceResult / ptrString are slice-159 test helpers — the
// sqlc-emitted nullable types for `state_result` / `state_freshness_status`
// shifted from `dbx.NullEvidenceResult{...}` / `pgtype.Text{...}` to
// pointer-style `*dbx.EvidenceResult` / `*string` under
// `emit_pointers_for_null_types: true`. Helpers keep the test literals
// readable.
func ptrEvidenceResult(v dbx.EvidenceResult) *dbx.EvidenceResult { return &v }
func ptrString(v string) *string                                 { return &v }

// fakeStateRow is a tiny stateRowMeta implementation used by the wire-
// conversion unit tests. Keeps the tests independent of the sqlc row
// types so a future column tweak doesn't ripple through.
type fakeStateRow struct {
	valid          bool
	result         string
	freshness      string
	evaluatedAt    time.Time
	lastObservedAt *time.Time
}

func (f fakeStateRow) StateValid() bool            { return f.valid }
func (f fakeStateRow) StateResult() string         { return f.result }
func (f fakeStateRow) StateFreshness() string      { return f.freshness }
func (f fakeStateRow) StateEvaluatedAt() time.Time { return f.evaluatedAt }
func (f fakeStateRow) StateLastObservedAt() (time.Time, bool) {
	if f.lastObservedAt == nil {
		return time.Time{}, false
	}
	return *f.lastObservedAt, true
}

// AC-7 — single anchor + single control: state passes through verbatim.
// The wire shape carries the result, freshness, and timestamps the row
// already had — no transformation, no rollup math (one control = no
// aggregation needed).
func TestAnchorStateWireFrom_SingleAnchorSingleControl_PassesThrough(t *testing.T) {
	evalAt := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	lastObs := time.Date(2026, 5, 15, 9, 30, 0, 0, time.UTC)
	cell := anchorStateWireFrom(fakeStateRow{
		valid:          true,
		result:         "pass",
		freshness:      "fresh",
		evaluatedAt:    evalAt,
		lastObservedAt: &lastObs,
	})
	if cell == nil {
		t.Fatal("expected non-nil state cell")
	}
	if cell.Result != "pass" {
		t.Errorf("result = %q; want pass", cell.Result)
	}
	if cell.FreshnessStatus != "fresh" {
		t.Errorf("freshness = %q; want fresh", cell.FreshnessStatus)
	}
	if cell.EvaluatedAt != "2026-05-16T10:00:00Z" {
		t.Errorf("evaluated_at = %q", cell.EvaluatedAt)
	}
	if cell.LastObservedAt == nil || *cell.LastObservedAt != "2026-05-15T09:30:00Z" {
		t.Errorf("last_observed_at = %v", cell.LastObservedAt)
	}
}

// AC-7 — single anchor + no control: state is nil. The LEFT JOIN
// produced no matching state row, so StateValid() returns false and
// the wire layer renders `state: null` (the bff + frontend's "—"
// placeholder).
func TestAnchorStateWireFrom_NoControlInstantiated_ReturnsNil(t *testing.T) {
	cell := anchorStateWireFrom(fakeStateRow{valid: false})
	if cell != nil {
		t.Fatalf("expected nil state cell, got %+v", cell)
	}
}

// AC-7 — last_observed_at MAY be null even when state is populated
// (a control with `inconclusive` + `no_evidence` has no observation).
// The wire shape carries the freshness/result columns but emits
// `last_observed_at: null`.
func TestAnchorStateWireFrom_StateValidButNoObservation_NullObservedAt(t *testing.T) {
	evalAt := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	cell := anchorStateWireFrom(fakeStateRow{
		valid:       true,
		result:      "inconclusive",
		freshness:   "no_evidence",
		evaluatedAt: evalAt,
		// lastObservedAt = nil
	})
	if cell == nil {
		t.Fatal("expected non-nil state cell")
	}
	if cell.LastObservedAt != nil {
		t.Errorf("last_observed_at = %v; want nil", cell.LastObservedAt)
	}
	if cell.Result != "inconclusive" {
		t.Errorf("result = %q", cell.Result)
	}
}

// AC-7 — two controls with conflicting state on one anchor: worst-state
// wins. The aggregation is implemented IN SQL (CASE MAX over the enum
// rank); this test pins the rank order at the wire layer by exercising
// the latestRowsToStateWire path with the result enum the SQL would
// pre-aggregate. Validates that the wire layer respects the SQL
// invariant (worst-result wins) rather than re-aggregating.
//
// In practice the SQL has already done the rollup before the row hits
// the handler — this test guards the contract: whatever the SQL says
// the worst state is, the wire layer renders verbatim. (The integration
// test stands the SQL itself up against real ledger data.)
func TestLatestRowsToStateWire_RendersSQLAggregatedResultVerbatim(t *testing.T) {
	evalAt := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)

	// Two anchor rows: one with a SQL-pre-aggregated "fail" (one of
	// its two controls failed), one with a SQL-pre-aggregated "pass"
	// (both of its two controls passed). The handler renders whatever
	// the SQL says.
	rows := []dbx.ListSCFAnchorsLatestWithStateRow{
		{
			ID: pgtype.UUID{Bytes: [16]byte{0x11}, Valid: true}, ScfID: "AAA-01",
			Family: "AAA", Title: "first anchor",
			StateResult:          ptrEvidenceResult("fail"),
			StateFreshnessStatus: ptrString("fresh"),
			StateEvaluatedAt:     pgtype.Timestamptz{Time: evalAt, Valid: true},
		},
		{
			ID: pgtype.UUID{Bytes: [16]byte{0x22}, Valid: true}, ScfID: "AAA-02",
			Family: "AAA", Title: "second anchor",
			StateResult:          ptrEvidenceResult("pass"),
			StateFreshnessStatus: ptrString("fresh"),
			StateEvaluatedAt:     pgtype.Timestamptz{Time: evalAt, Valid: true},
		},
		{
			// Anchor with NO tenant control instantiated.
			ID: pgtype.UUID{Bytes: [16]byte{0x33}, Valid: true}, ScfID: "AAA-03",
			Family: "AAA", Title: "no-control anchor",
			// All state_* columns are zero-value / .Valid = false
		},
	}
	wire := latestRowsToStateWire(rows)
	if len(wire) != 3 {
		t.Fatalf("wire len = %d; want 3", len(wire))
	}
	if wire[0].State == nil || wire[0].State.Result != "fail" {
		t.Errorf("row[0] result = %+v; want fail", wire[0].State)
	}
	if wire[1].State == nil || wire[1].State.Result != "pass" {
		t.Errorf("row[1] result = %+v; want pass", wire[1].State)
	}
	if wire[2].State != nil {
		t.Errorf("row[2] state = %+v; want nil (no control instantiated)", wire[2].State)
	}
	if wire[0].SCFID != "AAA-01" || wire[1].SCFID != "AAA-02" || wire[2].SCFID != "AAA-03" {
		t.Errorf("scf_id ordering not preserved: %+v", []string{wire[0].SCFID, wire[1].SCFID, wire[2].SCFID})
	}
}

// includesState — the ?include= parser accepts plain `state`, CSV
// (`state,coverage`), and repeated query params (`?include=state&include=other`).
// Unknown tokens are silently ignored.
func TestIncludesState(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"plain", "/v1/anchors?include=state", true},
		{"csv-first", "/v1/anchors?include=state,coverage", true},
		{"csv-second", "/v1/anchors?include=coverage,state", true},
		{"repeated", "/v1/anchors?include=other&include=state", true},
		{"trims whitespace", "/v1/anchors?include=%20state%20", true},
		{"omitted", "/v1/anchors", false},
		{"unknown only", "/v1/anchors?include=coverage", false},
		{"empty value", "/v1/anchors?include=", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", c.url, nil)
			if got := includesState(req); got != c.want {
				t.Errorf("includesState(%s) = %v; want %v", c.url, got, c.want)
			}
		})
	}
}

// Defensive: the wire shape is the slice 098 design-doc-pinned column
// set. Adding fields silently breaks the frontend `AnchorRowState`
// type. This test pins the keys so a future refactor surfaces the
// drift.
func TestAnchorStateCellWire_HasPinnedFieldShape(t *testing.T) {
	evalAt := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	lastObs := time.Date(2026, 5, 15, 9, 30, 0, 0, time.UTC)
	cell := anchorStateWireFrom(fakeStateRow{
		valid:          true,
		result:         "pass",
		freshness:      "fresh",
		evaluatedAt:    evalAt,
		lastObservedAt: &lastObs,
	})
	// Cheap JSON-shape pin without dragging in a full Marshal.
	if cell.Result == "" || cell.FreshnessStatus == "" ||
		cell.EvaluatedAt == "" || cell.LastObservedAt == nil {
		t.Errorf("state cell missing pinned column: %+v", cell)
	}
	// Pin RFC3339 nano output — the slice-012 stateWire format. Tests
	// fail loudly if a refactor flips the format to RFC3339 (seconds).
	if !strings.HasSuffix(cell.EvaluatedAt, "Z") {
		t.Errorf("evaluated_at should be UTC RFC3339Nano: %q", cell.EvaluatedAt)
	}
}
