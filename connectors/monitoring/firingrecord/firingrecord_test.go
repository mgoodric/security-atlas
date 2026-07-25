package firingrecord

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
	"github.com/mgoodric/security-atlas/connectors/monitoring/idem"
)

func sampleFiring() firing.Firing {
	fired := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	resolved := time.Date(2026, 6, 7, 10, 30, 0, 0, time.UTC)
	return firing.Firing{
		SourceVendor: firing.VendorDatadog,
		RuleID:       "mon-1",
		State:        firing.StateResolved,
		FiredAt:      fired,
		ResolvedAt:   resolved,
		Target:       &firing.Target{Kind: "slack", Name: "slack-sec-oncall"},
		ObservedAt:   time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuild_PopulatesRecord(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleFiring(), "scf:IRO-09", "connector:datadog:firing@dev", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.EvidenceKind != "monitoring.alert_firing.v1" || rec.SchemaVersion != "1.0.0" {
		t.Errorf("kind/version wrong: %q %q", rec.EvidenceKind, rec.SchemaVersion)
	}
	if rec.ControlId != "scf:IRO-09" {
		t.Errorf("control = %q", rec.ControlId)
	}
	if rec.SourceAttribution.ActorId != "connector:datadog:firing@dev" {
		t.Errorf("actor = %q", rec.SourceAttribution.ActorId)
	}
	fields := rec.Payload.GetFields()
	if fields["source_vendor"].GetStringValue() != "datadog" {
		t.Errorf("source_vendor = %q", fields["source_vendor"].GetStringValue())
	}
	if fields["rule_id"].GetStringValue() != "mon-1" {
		t.Errorf("rule_id = %q", fields["rule_id"].GetStringValue())
	}
	if fields["state"].GetStringValue() != "resolved" {
		t.Errorf("state = %q", fields["state"].GetStringValue())
	}
	if fields["fired_at"].GetStringValue() != "2026-06-07T10:00:00Z" {
		t.Errorf("fired_at = %q", fields["fired_at"].GetStringValue())
	}
	if fields["resolved_at"].GetStringValue() != "2026-06-07T10:30:00Z" {
		t.Errorf("resolved_at = %q", fields["resolved_at"].GetStringValue())
	}
	if fields["target_kind"].GetStringValue() != "slack" || fields["target_name"].GetStringValue() != "slack-sec-oncall" {
		t.Errorf("target wrong: %v", fields)
	}
}

func TestBuild_IdempotencyKeyMatchesSharedDeriver(t *testing.T) {
	t.Parallel()
	f := sampleFiring()
	rec, err := Build(f, "scf:IRO-09", "a", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	hour := f.ObservedAt.UTC().Truncate(time.Hour)
	want := idem.AlertFiringKey("datadog", f.RuleID, f.FiredAt, hour)
	if rec.IdempotencyKey != want {
		t.Errorf("idem key = %q; want %q", rec.IdempotencyKey, want)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
}

func TestBuild_OmitsAbsentResolvedAndTarget(t *testing.T) {
	t.Parallel()
	f := sampleFiring()
	f.ResolvedAt = time.Time{} // still firing
	f.Target = nil
	rec, err := Build(f, "scf:IRO-09", "a", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fields := rec.Payload.GetFields()
	if _, ok := fields["resolved_at"]; ok {
		t.Error("resolved_at present on a still-firing record")
	}
	if _, ok := fields["target_kind"]; ok {
		t.Error("target_kind present with nil target")
	}
}

func TestBuild_ResultIsInconclusive(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleFiring(), "c", "a", "datadog", "prod")
	if rec.Result.String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want INCONCLUSIVE (evaluator decides)", rec.Result)
	}
}
