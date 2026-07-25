package siemsignals

import (
	"strings"
	"testing"
	"time"
)

func sampleSignal() Signal {
	ts := time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)
	return Signal{
		SignalID: "sig-1", RuleID: "rule-aaa", RuleName: "Brute force",
		Severity: "high", Status: "archived",
		FirstSeenAt: ts.Add(-time.Hour), TriagedAt: ts, LastUpdatedAt: ts,
		TriagerHandle: "alice-sec",
		ObservedAt:    time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuild_RecordShape(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleSignal(), "scf:THR-01", "connector:datadog:siemsignals@dev", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != EvidenceKind || rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("kind/version wrong: %s %s", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	if rec.GetControlId() != "scf:THR-01" {
		t.Errorf("control_id = %q", rec.GetControlId())
	}
	if rec.GetResult().String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want INCONCLUSIVE (evaluator decides)", rec.GetResult())
	}
	// observed_at truncated to the hour.
	if got := rec.GetObservedAt().AsTime(); got != time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) {
		t.Errorf("observed_at = %v; want hour-truncated", got)
	}
	fields := rec.GetPayload().GetFields()
	for _, key := range []string{"signal_id", "rule_id", "rule_name", "severity", "status", "triager_handle", "first_seen_at", "triaged_at", "last_updated_at"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("payload missing %q", key)
		}
	}
	if fields["signal_id"].GetStringValue() != "sig-1" {
		t.Errorf("signal_id wrong: %v", fields["signal_id"])
	}
}

// TestBuild_NoOverCollectedFields proves the emitted payload carries NO
// over-collection key — the schema's additionalProperties:false plus this
// assertion pin the boundary (P0-636).
func TestBuild_NoOverCollectedFields(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleSignal(), "scf:THR-01", "a", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	banned := []string{"message", "samples", "sample", "query", "tags", "tag", "payload", "custom", "raw", "body", "email"}
	for key := range rec.GetPayload().GetFields() {
		low := strings.ToLower(key)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("payload carries over-collected key %q (banned substring %q)", key, b)
			}
		}
	}
}

func TestBuild_OmitsZeroTimestampsAndEmptyOptionals(t *testing.T) {
	t.Parallel()
	s := Signal{
		SignalID: "s", RuleID: "r", Severity: "low", Status: "open",
		ObservedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		// no rule name, no triager, no timestamps
	}
	rec, err := Build(s, "scf:THR-01", "a", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fields := rec.GetPayload().GetFields()
	for _, key := range []string{"rule_name", "triager_handle", "first_seen_at", "triaged_at", "last_updated_at"} {
		if _, ok := fields[key]; ok {
			t.Errorf("optional %q should be omitted when empty/zero", key)
		}
	}
	// required fields still present
	for _, key := range []string{"signal_id", "rule_id", "severity", "status"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("required %q missing", key)
		}
	}
}

func TestBuild_IdempotencyKeyStableWithinHour(t *testing.T) {
	t.Parallel()
	a := sampleSignal()
	b := a
	b.ObservedAt = a.ObservedAt.Add(20 * time.Minute) // same hour
	ra, _ := Build(a, "c", "x", "datadog", "prod")
	rb, _ := Build(b, "c", "x", "datadog", "prod")
	if ra.GetIdempotencyKey() != rb.GetIdempotencyKey() {
		t.Error("idempotency key should be stable within the observed hour")
	}
	c := a
	c.ObservedAt = a.ObservedAt.Add(2 * time.Hour) // next hour
	rc, _ := Build(c, "c", "x", "datadog", "prod")
	if ra.GetIdempotencyKey() == rc.GetIdempotencyKey() {
		t.Error("idempotency key should differ across hours")
	}
}
