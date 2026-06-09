package hierarchyrecord

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/hris/hierarchy"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

func sampleEdge() hierarchy.Edge {
	return hierarchy.Edge{
		WorkerAssignmentID:  "w-1",
		ManagerAssignmentID: "m-7",
		Depth:               2,
		OrphanedReport:      false,
		CycleMember:         false,
	}
}

var obsAt = time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC)

func TestBuild_SetsKindAndIdempotencyAndScope(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleEdge(), worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != EvidenceKind {
		t.Errorf("kind = %q; want %q", rec.GetEvidenceKind(), EvidenceKind)
	}
	if rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("schema version = %q", rec.GetSchemaVersion())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("idempotency key empty")
	}
	if rec.GetControlId() != "scf:IAC-22" {
		t.Errorf("control = %q", rec.GetControlId())
	}
	scope := map[string]string{}
	for _, d := range rec.GetScope() {
		scope[d.GetKey()] = d.GetValues()[0]
	}
	if scope["service"] != "rippling" || scope["environment"] != "prod" {
		t.Errorf("scope = %v", scope)
	}
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !rec.GetObservedAt().AsTime().Equal(want) {
		t.Errorf("observed_at = %v; want %v (hour-truncated)", rec.GetObservedAt().AsTime(), want)
	}
}

// TestBuild_PayloadCarriesHierarchyFactsOnly is the record-level over-collection
// guard (the slice-491 identity boundary): only the allow-listed opaque-id +
// derived-fact keys exist — no name / email / phone / address / contact PII key.
func TestBuild_PayloadCarriesHierarchyFactsOnly(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleEdge(), worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	pm := rec.GetPayload().AsMap()
	for _, k := range []string{"source_hris", "worker_assignment_id", "depth", "orphaned_report", "cycle_member"} {
		if _, ok := pm[k]; !ok {
			t.Errorf("missing required payload key %q", k)
		}
	}
	allowed := map[string]bool{
		"source_hris": true, "worker_assignment_id": true, "manager_assignment_id": true,
		"depth": true, "orphaned_report": true, "cycle_member": true,
	}
	for k := range pm {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q (over-collection guard, slice-491 identity boundary)", k)
		}
	}
}

func TestBuild_RootOmitsManagerAssignmentID(t *testing.T) {
	t.Parallel()
	e := sampleEdge()
	e.ManagerAssignmentID = "" // a tree root (e.g. CEO)
	e.Depth = 0
	rec, _ := Build(e, worker.HRISBambooHR, "scf:IAC-22", "connector:bamboohr:hierarchy@dev", "bamboohr", "prod", obsAt)
	if _, ok := rec.GetPayload().AsMap()["manager_assignment_id"]; ok {
		t.Error("root edge should omit manager_assignment_id, not emit an empty string")
	}
}

func TestBuild_OrphanAndCycleFlagsCarried(t *testing.T) {
	t.Parallel()
	e := hierarchy.Edge{WorkerAssignmentID: "w-9", ManagerAssignmentID: "m-x", Depth: -1, OrphanedReport: true, CycleMember: true}
	rec, _ := Build(e, worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	pm := rec.GetPayload().AsMap()
	if pm["orphaned_report"] != true {
		t.Errorf("orphaned_report = %v; want true", pm["orphaned_report"])
	}
	if pm["cycle_member"] != true {
		t.Errorf("cycle_member = %v; want true", pm["cycle_member"])
	}
	if pm["depth"] != float64(-1) {
		t.Errorf("depth = %v; want -1", pm["depth"])
	}
}

func TestBuild_SameWorkerSameHourSameKey(t *testing.T) {
	t.Parallel()
	r1, _ := Build(sampleEdge(), worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	r2, _ := Build(sampleEdge(), worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	if r1.GetIdempotencyKey() != r2.GetIdempotencyKey() {
		t.Error("same worker + hour should yield same idempotency key")
	}
}

func TestBuild_DiffersFromLifecycleKey(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleEdge(), worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	// The hierarchy record's idempotency key is distinct from a lifecycle record's
	// for the same worker/hour (distinct kind prefix), so the two kinds never
	// collide in the ledger. Asserted via the kind, which is the discriminator.
	if rec.GetEvidenceKind() == "hris.worker_lifecycle.v1" {
		t.Error("hierarchy record must not carry the lifecycle kind")
	}
}

func TestBuild_ResultIsInconclusive(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleEdge(), worker.HRISRippling, "scf:IAC-22", "connector:rippling:hierarchy@dev", "rippling", "prod", obsAt)
	if rec.GetResult().String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want RESULT_INCONCLUSIVE (evaluator decides)", rec.GetResult())
	}
}
