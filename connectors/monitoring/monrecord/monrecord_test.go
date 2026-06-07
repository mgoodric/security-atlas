package monrecord

import (
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
)

func sampleRule() alertcfg.Rule {
	return alertcfg.Rule{
		SourceVendor: alertcfg.VendorDatadog,
		RuleID:       "mon-1", RuleName: "API 5xx", RuleType: "metric alert", Enabled: true,
		Folder:     "Prod",
		Targets:    []alertcfg.Target{{Kind: "slack", Name: "#sec-oncall"}},
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
	rec, err := Build(sampleRule(), "scf:MON-01", "connector:datadog:monitors@dev", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.EvidenceKind != EvidenceKind {
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
	if rec.GetSourceAttribution().GetActorId() != "connector:datadog:monitors@dev" {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if scopeValue(rec.GetScope(), "service") != "datadog" || scopeValue(rec.GetScope(), "environment") != "prod" {
		t.Errorf("scope wrong: %v", rec.GetScope())
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"source_vendor", "rule_id", "rule_name", "rule_type", "enabled", "folder", "notification_targets"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
	if pl["source_vendor"] != "datadog" {
		t.Errorf("source_vendor = %v", pl["source_vendor"])
	}
}

func TestBuild_OmitsEmptyOptionals(t *testing.T) {
	t.Parallel()
	r := sampleRule()
	r.Folder = ""
	r.Targets = nil
	rec, _ := Build(r, "scf:MON-01", "connector:grafana:alerts@dev", "grafana", "prod")
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"folder", "notification_targets"} {
		if _, ok := pl[k]; ok {
			t.Errorf("empty optional %q should be omitted", k)
		}
	}
}

// TestBuild_PayloadIsConfigOnly pins P0-488-3 / AC-10 at the builder boundary:
// only the allow-listed config / target-name keys may appear, and no key may
// contain a secret-flavoured substring.
func TestBuild_PayloadIsConfigOnly(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"source_vendor": true, "rule_id": true, "rule_name": true, "rule_type": true,
		"enabled": true, "folder": true, "notification_targets": true,
	}
	// A target whose NAME is a real channel name is fine; assert no top-level
	// key is a secret container.
	banned := []string{"url", "secret", "token", "password", "webhook", "email", "recipient", "query", "dashboard", "metric"}
	rec, _ := Build(sampleRule(), "scf:MON-01", "a", "datadog", "prod")
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
	// notification_targets entries expose only target_kind + target_name.
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
