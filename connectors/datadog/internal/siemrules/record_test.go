package siemrules

import (
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

func sampleRule() Rule {
	return Rule{
		RuleID: "rule-1", RuleName: "Brute force", DetectionClass: "log",
		Enabled: true, Severity: "high",
		Targets:    []Target{{Kind: "slack", Name: "slack-sec-oncall"}},
		ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}

func TestBuild_Shape(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleRule(), "scf:THR-01", "connector:datadog:siemrules@dev", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.EvidenceKind != EvidenceKind || EvidenceKind != "datadog.siem_rule.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %q", rec.SchemaVersion)
	}
	if rec.Result != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %v; want INCONCLUSIVE", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if rec.GetSourceAttribution().GetActorId() != "connector:datadog:siemrules@dev" {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if scopeValue(rec.GetScope(), "service") != "datadog" || scopeValue(rec.GetScope(), "environment") != "prod" {
		t.Errorf("scope wrong: %v", rec.GetScope())
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"rule_id", "rule_name", "detection_class", "enabled", "severity", "notification_targets"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
	if pl["severity"] != "high" || pl["detection_class"] != "log" {
		t.Errorf("severity/class wrong: %v", pl)
	}
}

func TestBuild_OmitsEmptyTargets(t *testing.T) {
	t.Parallel()
	r := sampleRule()
	r.Targets = nil
	rec, _ := Build(r, "scf:THR-01", "a", "datadog", "prod")
	if _, ok := rec.GetPayload().AsMap()["notification_targets"]; ok {
		t.Error("empty notification_targets should be omitted")
	}
}

// TestBuild_PayloadIsConfigOnly pins P0-533 at the builder boundary: only the
// allow-listed config / target-name keys may appear, and no key may contain a
// secret/signal-flavoured substring.
func TestBuild_PayloadIsConfigOnly(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"rule_id": true, "rule_name": true, "detection_class": true,
		"enabled": true, "severity": true, "notification_targets": true,
	}
	banned := []string{
		"url", "secret", "token", "password", "webhook", "email", "recipient",
		"query", "signal", "sample", "event", "payload", "condition", "filter",
	}
	rec, _ := Build(sampleRule(), "scf:THR-01", "a", "datadog", "prod")
	for k := range rec.GetPayload().AsMap() {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q", k)
		}
		low := strings.ToLower(k)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("payload key %q contains banned substring %q", k, b)
			}
		}
	}
	targets, _ := rec.GetPayload().AsMap()["notification_targets"].([]any)
	for _, ti := range targets {
		m, _ := ti.(map[string]any)
		for k := range m {
			if k != "target_kind" && k != "target_name" {
				t.Errorf("notification target exposes unexpected key %q", k)
			}
		}
	}
}

func TestBuild_DedupKeyStableWithinHour(t *testing.T) {
	t.Parallel()
	r1 := sampleRule()
	r2 := sampleRule()
	r2.ObservedAt = r2.ObservedAt.Add(20 * time.Minute) // same hour
	rec1, _ := Build(r1, "c", "a", "datadog", "prod")
	rec2, _ := Build(r2, "c", "a", "datadog", "prod")
	if rec1.IdempotencyKey != rec2.IdempotencyKey {
		t.Error("same rule in same hour should share idempotency key")
	}
}

func TestBuild_DedupKeyDiffersAcrossRules(t *testing.T) {
	t.Parallel()
	r1 := sampleRule()
	r2 := sampleRule()
	r2.RuleID = "rule-2"
	rec1, _ := Build(r1, "c", "a", "datadog", "prod")
	rec2, _ := Build(r2, "c", "a", "datadog", "prod")
	if rec1.IdempotencyKey == rec2.IdempotencyKey {
		t.Error("different rules should not share idempotency key")
	}
}
