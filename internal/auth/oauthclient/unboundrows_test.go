package oauthclient

import (
	"strings"
	"testing"
)

// Pure-Go tests for the all-zeros purge bookkeeping (security-atlas#1876).
// The database paths run in unboundrows_integration_test.go.

func TestPurgeableRows_SkipsRetained(t *testing.T) {
	t.Parallel()
	counts := []UnboundTableCount{
		{Table: "controls", Rows: 2},
		{Table: "evidence_records", Rows: 40, Retained: RetainedLedger},
		{Table: "risks", Rows: 3},
	}
	if got := PurgeableRows(counts); got != 5 {
		t.Errorf("PurgeableRows = %d, want 5", got)
	}
	if got := PurgeableRows(nil); got != 0 {
		t.Errorf("PurgeableRows(nil) = %d, want 0", got)
	}
}

func TestComparePurge(t *testing.T) {
	t.Parallel()
	before := []UnboundTableCount{
		{Table: "controls", Rows: 2},
		{Table: "evidence_records", Rows: 40, Retained: RetainedLedger},
	}
	cases := []struct {
		name    string
		after   []UnboundTableCount
		wantErr string
	}{
		{name: "purged, ledger untouched", after: []UnboundTableCount{{Table: "evidence_records", Rows: 40, Retained: RetainedLedger}}},
		{name: "purgeable rows left", after: []UnboundTableCount{
			{Table: "controls", Rows: 1},
			{Table: "evidence_records", Rows: 40, Retained: RetainedLedger},
		}, wantErr: "left 1"},
		{name: "ledger changed", after: []UnboundTableCount{{Table: "evidence_records", Rows: 39, Retained: RetainedLedger}}, wantErr: "changed retained table evidence_records"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := comparePurge(before, tc.after)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(res.Purged) != 1 || res.Purged[0].Table != "controls" || len(res.Retained) != 1 {
				t.Errorf("result = %+v, want controls purged and evidence_records retained", res)
			}
		})
	}
}

func TestCompareLedgerTotals(t *testing.T) {
	t.Parallel()
	before := map[string]int64{"evidence_records": 40, "change_audit_log": 3}
	if err := compareLedgerTotals(before, map[string]int64{"evidence_records": 40, "change_audit_log": 3}); err != nil {
		t.Errorf("unchanged ledgers: err = %v", err)
	}
	err := compareLedgerTotals(before, map[string]int64{"evidence_records": 39, "change_audit_log": 3})
	if err == nil || !strings.Contains(err.Error(), "changed ledger table evidence_records (40 rows across all tenants, now 39)") {
		t.Errorf("changed ledger: err = %v", err)
	}
	if err := compareLedgerTotals(before, map[string]int64{"evidence_records": 40}); err == nil {
		t.Error("ledger missing after purge: err = nil, want a change reported")
	}
}

func TestTableRefString(t *testing.T) {
	t.Parallel()
	if got := (tableRef{schema: "public", name: "controls"}).String(); got != "controls" {
		t.Errorf("public table = %q", got)
	}
	if got := (tableRef{schema: "ext", name: "controls"}).String(); got != "ext.controls" {
		t.Errorf("other schema = %q", got)
	}
	if got := (tableRef{schema: "public", name: "controls"}).ident(); got != `"public"."controls"` {
		t.Errorf("ident = %q", got)
	}
}
