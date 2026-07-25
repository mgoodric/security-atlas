package pdrecord

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/metrics"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/postmortems"
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

func TestBuildPostmortem(t *testing.T) {
	t.Parallel()
	p := postmortems.Postmortem{
		ID: "PM1", IncidentID: "INC1", Status: "published",
		CreatedAt:       time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
		PublishedAt:     time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC),
		ActionItemCount: 3, ActionItemsDone: 2, ActionItemsOpen: 1,
	}
	rec, err := BuildPostmortem(p, "scf:IRO-13", "connector:pagerduty:postmortems@dev", "pagerduty", "prod", fixedNow)
	if err != nil {
		t.Fatalf("BuildPostmortem: %v", err)
	}
	if rec.GetEvidenceKind() != PostmortemKind || rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("kind/version = %q/%q", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("empty idempotency key")
	}
	if got := rec.GetObservedAt().AsTime(); got != fixedNow.Truncate(time.Hour) {
		t.Errorf("observed_at = %v", got)
	}
	pm := rec.GetPayload().AsMap()
	if pm["postmortem_id"] != "PM1" || pm["incident_id"] != "INC1" || pm["status"] != "published" {
		t.Errorf("payload = %v", pm)
	}
	// structpb numbers come back as float64.
	if pm["action_item_count"] != float64(3) || pm["action_items_completed"] != float64(2) || pm["action_items_open"] != float64(1) {
		t.Errorf("rollup payload = %v", pm)
	}
	if pm["published_at"] != "2026-05-03T12:00:00Z" {
		t.Errorf("published_at = %v", pm["published_at"])
	}

	// No payload key can carry the narrative / an action-item title (P0-538):
	// the only payload keys are the known metadata keys.
	allowed := map[string]bool{
		"postmortem_id": true, "incident_id": true, "status": true, "created_at": true,
		"published_at": true, "action_item_count": true, "action_items_completed": true, "action_items_open": true,
	}
	for k := range pm {
		if !allowed[k] {
			t.Errorf("unexpected payload key %q — postmortem payload must be metadata-only", k)
		}
	}
}

func TestBuildPostmortem_OmitsPublishedWhenUnpublished(t *testing.T) {
	t.Parallel()
	p := postmortems.Postmortem{ID: "PM2", IncidentID: "INC2", Status: "in_progress",
		CreatedAt: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	rec, _ := BuildPostmortem(p, "c", "actor", "pagerduty", "prod", fixedNow)
	pm := rec.GetPayload().AsMap()
	if _, ok := pm["published_at"]; ok {
		t.Error("published_at should be omitted when unpublished")
	}
	// The zero-action-item rollup is still emitted (a published-zero is a fact).
	if pm["action_item_count"] != float64(0) {
		t.Errorf("action_item_count = %v; want 0", pm["action_item_count"])
	}
}

func TestBuildPostmortem_Idempotent(t *testing.T) {
	t.Parallel()
	p := postmortems.Postmortem{ID: "PM1", IncidentID: "INC1", Status: "published"}
	a, _ := BuildPostmortem(p, "c", "actor", "pagerduty", "prod", fixedNow)
	b, _ := BuildPostmortem(p, "c", "actor", "pagerduty", "prod", fixedNow.Add(30*time.Minute))
	if a.GetIdempotencyKey() != b.GetIdempotencyKey() {
		t.Error("same postmortem within the hour should share an idempotency key")
	}
}

func TestBuildResponseMetrics(t *testing.T) {
	t.Parallel()
	m := metrics.ServiceMetrics{
		ServiceID:         "SVCA",
		IncidentCount:     5,
		AcknowledgedCount: 4,
		ResolvedCount:     3,
		MTTASecondsMean:   120, MTTASecondsP50: 100, MTTASecondsP90: 180, MTTASecondsP95: 200,
		MTTRSecondsMean: 1200, MTTRSecondsP50: 1000, MTTRSecondsP90: 1800, MTTRSecondsP95: 2000,
	}
	since := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	rec, err := BuildResponseMetrics(m, "scf:IRO-02", "connector:pagerduty:metrics@dev", "pagerduty", "prod", since, until, fixedNow)
	if err != nil {
		t.Fatalf("BuildResponseMetrics: %v", err)
	}
	if rec.GetEvidenceKind() != MetricsKind || rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("kind/version = %q/%q", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("empty idempotency key")
	}
	if got := rec.GetObservedAt().AsTime(); got != fixedNow.Truncate(time.Hour) {
		t.Errorf("observed_at = %v", got)
	}
	pm := rec.GetPayload().AsMap()
	if pm["service_id"] != "SVCA" {
		t.Errorf("service_id = %v", pm["service_id"])
	}
	if pm["window_start"] != "2026-03-09T00:00:00Z" || pm["window_end"] != "2026-06-07T00:00:00Z" {
		t.Errorf("window = %v..%v", pm["window_start"], pm["window_end"])
	}
	if pm["incident_count"] != float64(5) || pm["acknowledged_count"] != float64(4) || pm["resolved_count"] != float64(3) {
		t.Errorf("counts = %v", pm)
	}
	mtta, ok := pm["mtta_seconds"].(map[string]any)
	if !ok {
		t.Fatalf("mtta_seconds not an object: %T", pm["mtta_seconds"])
	}
	if mtta["mean"] != float64(120) || mtta["p95"] != float64(200) {
		t.Errorf("mtta = %v", mtta)
	}
	mttr, ok := pm["mttr_seconds"].(map[string]any)
	if !ok {
		t.Fatalf("mttr_seconds not an object: %T", pm["mttr_seconds"])
	}
	if mttr["mean"] != float64(1200) || mttr["p90"] != float64(1800) {
		t.Errorf("mttr = %v", mttr)
	}

	// AGGREGATE-ONLY (P0-539): the only payload keys are the known aggregate
	// keys — no responder-identity key can appear.
	allowed := map[string]bool{
		"service_id": true, "window_start": true, "window_end": true,
		"incident_count": true, "acknowledged_count": true, "resolved_count": true,
		"mtta_seconds": true, "mttr_seconds": true,
	}
	for k := range pm {
		if !allowed[k] {
			t.Errorf("unexpected payload key %q — response-metrics payload must be aggregate-only (P0-539)", k)
		}
	}
}

func TestBuildResponseMetrics_Idempotent(t *testing.T) {
	t.Parallel()
	m := metrics.ServiceMetrics{ServiceID: "SVCA", IncidentCount: 1}
	since := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	a, _ := BuildResponseMetrics(m, "c", "actor", "pagerduty", "prod", since, until, fixedNow)
	b, _ := BuildResponseMetrics(m, "c", "actor", "pagerduty", "prod", since, until, fixedNow.Add(30*time.Minute))
	if a.GetIdempotencyKey() != b.GetIdempotencyKey() {
		t.Error("same service within the hour should share an idempotency key")
	}
}
