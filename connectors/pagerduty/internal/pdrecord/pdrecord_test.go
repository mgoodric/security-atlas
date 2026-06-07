package pdrecord

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
)

var fixedNow = time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

func TestBuildOnCall(t *testing.T) {
	t.Parallel()
	p := oncall.Policy{
		ID: "PABC", Name: "Primary", NumTier: 1, Covered: true,
		Tiers: []oncall.Tier{{Level: 1, OnCall: []oncall.OnCall{{Kind: "user", ID: "U1", Name: "Alice"}}}},
	}
	rec, err := BuildOnCall(p, "scf:IRO-04", "connector:pagerduty:oncall@dev", "pagerduty", "prod", fixedNow)
	if err != nil {
		t.Fatalf("BuildOnCall: %v", err)
	}
	if rec.GetEvidenceKind() != OnCallKind || rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("kind/version = %q/%q", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("empty idempotency key")
	}
	// observed_at truncated to the hour.
	if got := rec.GetObservedAt().AsTime(); got != fixedNow.Truncate(time.Hour) {
		t.Errorf("observed_at = %v", got)
	}
	pm := rec.GetPayload().AsMap()
	if pm["escalation_policy_id"] != "PABC" || pm["covered"] != true {
		t.Errorf("payload = %v", pm)
	}
}

func TestBuildOnCall_Idempotent(t *testing.T) {
	t.Parallel()
	p := oncall.Policy{ID: "PABC", Name: "Primary", NumTier: 0, Covered: false}
	a, _ := BuildOnCall(p, "c", "actor", "pagerduty", "prod", fixedNow)
	b, _ := BuildOnCall(p, "c", "actor", "pagerduty", "prod", fixedNow.Add(30*time.Minute))
	if a.GetIdempotencyKey() != b.GetIdempotencyKey() {
		t.Error("same policy within the hour should share an idempotency key")
	}
}

func TestBuildIncident(t *testing.T) {
	t.Parallel()
	in := incidents.Incident{
		ID: "INC1", Number: 42, Status: "resolved", Urgency: "high",
		ServiceID: "SVC1", ServiceName: "API",
		CreatedAt:  time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
		ResolvedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
	}
	rec, err := BuildIncident(in, "scf:IRO-02", "connector:pagerduty:incidents@dev", "pagerduty", "prod", fixedNow)
	if err != nil {
		t.Fatalf("BuildIncident: %v", err)
	}
	if rec.GetEvidenceKind() != IncidentKind {
		t.Errorf("kind = %q", rec.GetEvidenceKind())
	}
	pm := rec.GetPayload().AsMap()
	if pm["incident_id"] != "INC1" || pm["status"] != "resolved" {
		t.Errorf("payload = %v", pm)
	}
	if pm["resolved_at"] != "2026-05-01T10:00:00Z" {
		t.Errorf("resolved_at = %v", pm["resolved_at"])
	}
}

func TestBuildIncident_OmitsOptionalWhenZero(t *testing.T) {
	t.Parallel()
	in := incidents.Incident{ID: "INC2", Status: "triggered", Urgency: "high",
		CreatedAt: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	rec, _ := BuildIncident(in, "c", "actor", "pagerduty", "prod", fixedNow)
	pm := rec.GetPayload().AsMap()
	if _, ok := pm["resolved_at"]; ok {
		t.Error("resolved_at should be omitted when unresolved")
	}
	if _, ok := pm["incident_number"]; ok {
		t.Error("incident_number should be omitted when zero")
	}
	if _, ok := pm["service_id"]; ok {
		t.Error("service_id should be omitted when empty")
	}
}
