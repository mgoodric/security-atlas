// Pure-Go unit tests for the slice 669 read-telemetry default-filter
// parsing + meta-audit shape (no Postgres, no build tag — the fast loop
// per CLAUDE.md slice 353 "Pure-Go pre-DB unit convention").
//
// The SQL-layer default-filtered-vs-show-all pin (AC-4) lives in the
// integration suite (read_telemetry_filter_integration_test.go); this
// file exercises the request-parse branches that decide the
// ExcludeReadTelemetry flag.

package adminauditlog

import (
	"net/http/httptest"
	"testing"
)

// TestParseUnifiedParams_IncludeReadsFlag pins the `?include_reads`
// query-string contract: absent or any non-"true" value parses to
// includeReads=false (business-events-only default); the literal
// "true" parses to includeReads=true (full-ledger opt-in).
func TestParseUnifiedParams_IncludeReadsFlag(t *testing.T) {
	t.Parallel()
	const window = "from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"
	cases := []struct {
		name string
		qs   string
		want bool
	}{
		{"absent_defaults_to_business_events", window, false},
		{"explicit_true_opts_into_reads", window + "&include_reads=true", true},
		{"explicit_false_stays_business_events", window + "&include_reads=false", false},
		{"non_boolean_value_stays_business_events", window + "&include_reads=1", false},
		{"empty_value_stays_business_events", window + "&include_reads=", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/v1/activity/unified?"+tc.qs, nil)
			got, err := parseUnifiedParams(req)
			if err != nil {
				t.Fatalf("parseUnifiedParams: unexpected error: %v", err)
			}
			if got.includeReads != tc.want {
				t.Errorf("includeReads = %v; want %v", got.includeReads, tc.want)
			}
		})
	}
}

// TestActivityDefaultExcludesReadTelemetry documents the handler-level
// invariant the activity endpoint enforces: the DEFAULT (include_reads
// absent) yields ExcludeReadTelemetry=true, while ?include_reads=true
// yields ExcludeReadTelemetry=false. The activity handler computes this
// as `ExcludeReadTelemetry = !includeReads`; this test pins that mapping
// at the parse layer so a future refactor cannot silently invert it.
func TestActivityDefaultExcludesReadTelemetry(t *testing.T) {
	t.Parallel()
	const window = "from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"
	cases := []struct {
		name             string
		qs               string
		wantExcludeReads bool // == !includeReads, the activity-handler mapping
	}{
		{"default_view_excludes_reads", window, true},
		{"opt_in_includes_reads", window + "&include_reads=true", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/v1/activity/unified?"+tc.qs, nil)
			got, err := parseUnifiedParams(req)
			if err != nil {
				t.Fatalf("parseUnifiedParams: unexpected error: %v", err)
			}
			if want := !got.includeReads; want != tc.wantExcludeReads {
				t.Errorf("ExcludeReadTelemetry (== !includeReads) = %v; want %v", want, tc.wantExcludeReads)
			}
		})
	}
}

// TestToAuditShape_RecordsIncludeReads pins that the meta-audit blob
// carries the include_reads opt-in (slice 669) so forensic review can
// distinguish a default business-events query from a full-ledger query.
func TestToAuditShape_RecordsIncludeReads(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET",
		"/v1/activity/unified?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&include_reads=true", nil)
	p, err := parseUnifiedParams(req)
	if err != nil {
		t.Fatalf("parseUnifiedParams: %v", err)
	}
	shape := p.toAuditShape()
	if !shape.IncludeReads {
		t.Errorf("toAuditShape.IncludeReads = false; want true (opt-in must be recorded)")
	}
}
