//go:build integration

// Slice 669 — integration tests for the Activity-feed read-telemetry
// default filter (`GET /v1/activity/unified`). Requires Postgres reachable
// via DATABASE_URL_APP — shares the TestMain bootstrap with the slice 124 /
// slice 270 suites (handler_integration_test.go).
//
// These tests pin AC-1, AC-2, and AC-4 at the SQL layer:
//
//   - AC-1: the DEFAULT activity view excludes `decision`/`read`
//     telemetry while keeping mutating/business events.
//   - AC-2: `?include_reads=true` surfaces the read-telemetry again — the
//     full ledger stays reachable (the filter is opt-in, not a deletion).
//   - AC-4: the underlying ledger is unchanged — the SAME seeded rows are
//     visible under show-all that were filtered out of the default view;
//     the filter is a view concern, not a retention change.
//
// The threat-model guard (a security-relevant mutation is NEVER hidden by
// the default) is pinned by seeding an exception write + a `decision`/
// non-read row and asserting both survive the default filter.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedDecisionRow inserts a decision_audit_log row with an explicit
// action verb. action='read' is the high-volume internal read-telemetry
// the slice 669 default filter targets; any other verb (e.g. 'write',
// 'approve') is a business mutation that must survive the filter.
func seedDecisionRow(t *testing.T, tenantID uuid.UUID, action string) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed set_config: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO decision_audit_log
		   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
		 VALUES (gen_random_uuid(), $1, 'seeder', $2, 'evidence', 'r-669', 'allow')`,
		tenantID, action,
	); err != nil {
		t.Fatalf("seed decision_audit_log: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

// queryActivity runs the activity endpoint as an admin caller (privileged
// short-circuit keeps row-visibility unchanged so the test isolates the
// slice 669 read-telemetry filter). includeReads toggles the
// `?include_reads=true` opt-in.
func queryActivity(t *testing.T, tenantID uuid.UUID, includeReads bool) []struct {
	Kind   string
	Action string
} {
	t.Helper()
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to)
	if includeReads {
		url += "&include_reads=true"
	}
	r := newActivityRouter(t, tenantID, uuid.New().String(), true /*isAdmin*/)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Entries []struct {
			Kind   string `json:"kind"`
			Action string `json:"action"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make([]struct {
		Kind   string
		Action string
	}, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, struct {
			Kind   string
			Action string
		}{e.Kind, e.Action})
	}
	return out
}

// TestSlice669_DefaultExcludesReadTelemetry pins AC-1: the default
// Activity view (include_reads absent) hides `decision`/`read` rows but
// keeps every business event.
func TestSlice669_DefaultExcludesReadTelemetry(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	// Noise: a decision/read row (internal authz read-telemetry).
	seedDecisionRow(t, tenant, "read")
	// Business events that MUST survive the default filter.
	seedDecisionRow(t, tenant, "write")              // a mutating authz decision
	seedUnifiedRow(t, tenant, "evidence_audit_log")  // evidence write
	seedUnifiedRow(t, tenant, "exception_audit_log") // security-relevant mutation

	rows := queryActivity(t, tenant, false /*default view*/)

	sawDecisionRead := false
	sawDecisionWrite := false
	sawEvidence := false
	sawException := false
	for _, e := range rows {
		switch {
		case e.Kind == "decision" && e.Action == "read":
			sawDecisionRead = true
		case e.Kind == "decision" && e.Action == "write":
			sawDecisionWrite = true
		case e.Kind == "evidence":
			sawEvidence = true
		case e.Kind == "exception":
			sawException = true
		}
	}

	if sawDecisionRead {
		t.Errorf("AC-1 violation: default Activity view surfaced a decision/read telemetry row")
	}
	if !sawDecisionWrite {
		t.Errorf("AC-1: default view dropped a decision/write business event (must survive)")
	}
	if !sawEvidence {
		t.Errorf("AC-1: default view dropped an evidence business event (must survive)")
	}
	if !sawException {
		t.Errorf("threat-model violation: default view hid a security-relevant exception mutation")
	}
}

// TestSlice669_IncludeReadsRestoresFullLedger pins AC-2 + AC-4: the same
// decision/read row hidden by the default view IS returned under
// ?include_reads=true — the ledger is unchanged, the filter is opt-in,
// and the read-telemetry stays reachable.
func TestSlice669_IncludeReadsRestoresFullLedger(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	seedDecisionRow(t, tenant, "read")
	seedDecisionRow(t, tenant, "write")

	// Default view: read row hidden.
	defaultRows := queryActivity(t, tenant, false)
	defaultReads := 0
	for _, e := range defaultRows {
		if e.Kind == "decision" && e.Action == "read" {
			defaultReads++
		}
	}
	if defaultReads != 0 {
		t.Fatalf("precondition: default view should hide read-telemetry; saw %d", defaultReads)
	}

	// Show-all view: read row reappears (AC-2). The decision/write business
	// event is present in BOTH views (AC-4 — the filter only adds the read
	// rows back; it never changes the business-event set).
	allRows := queryActivity(t, tenant, true)
	allReads := 0
	allWrites := 0
	for _, e := range allRows {
		switch {
		case e.Kind == "decision" && e.Action == "read":
			allReads++
		case e.Kind == "decision" && e.Action == "write":
			allWrites++
		}
	}
	if allReads == 0 {
		t.Errorf("AC-2: ?include_reads=true did NOT surface the read-telemetry row; the full ledger must stay reachable")
	}
	if allWrites == 0 {
		t.Errorf("AC-4: the decision/write business event must be present in the show-all view too")
	}
}

// TestSlice669_ShowAllSupersetsDefault pins AC-4 explicitly: the show-all
// result set is a strict superset of the default-filtered set — the
// difference is exactly the read-telemetry rows. No business event is
// added or dropped by toggling the filter; the ledger is the same.
func TestSlice669_ShowAllSupersetsDefault(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	seedDecisionRow(t, tenant, "read")
	seedDecisionRow(t, tenant, "read")
	seedDecisionRow(t, tenant, "approve") // business mutation
	seedUnifiedRow(t, tenant, "evidence_audit_log")

	defaultRows := queryActivity(t, tenant, false)
	allRows := queryActivity(t, tenant, true)

	if len(allRows) <= len(defaultRows) {
		t.Errorf("AC-4: show-all (%d) must be a strict superset of default (%d) when read-telemetry exists",
			len(allRows), len(defaultRows))
	}
	// The default view must contain ZERO decision/read rows; show-all must
	// contain the same non-read rows the default view had.
	for _, e := range defaultRows {
		if e.Kind == "decision" && e.Action == "read" {
			t.Errorf("AC-4: default view leaked a read-telemetry row")
		}
	}
}
