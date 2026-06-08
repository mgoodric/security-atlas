package workerrecord

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

func sampleWorker() worker.Worker {
	return worker.Worker{
		SourceHRIS:          worker.HRISRippling,
		WorkerID:            "w-1",
		Status:              worker.StatusActive,
		StartDate:           time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		EndDate:             time.Time{},
		Title:               "Software Engineer",
		Department:          "Engineering",
		ManagerAssignmentID: "m-7",
		WorkEmail:           "a.engineer@corp.example",
		ObservedAt:          time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuild_SetsKindAndIdempotencyAndScope(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleWorker(), "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
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
		t.Errorf("observed_at = %v; want %v", rec.GetObservedAt().AsTime(), want)
	}
}

// TestBuild_PayloadCarriesLifecycleFactsOnly is the record-level over-collection
// guard (AC-10 / P0-491-3): only the allow-listed worker-lifecycle keys exist —
// no SSN / compensation / address / bank / benefits / performance / DOB / contact
// PII key.
func TestBuild_PayloadCarriesLifecycleFactsOnly(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleWorker(), "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
	pm := rec.GetPayload().AsMap()
	for _, k := range []string{"source_hris", "worker_id", "employment_status"} {
		if _, ok := pm[k]; !ok {
			t.Errorf("missing required payload key %q", k)
		}
	}
	allowed := map[string]bool{
		"source_hris": true, "worker_id": true, "employment_status": true,
		"start_date": true, "end_date": true, "title": true, "department": true,
		"manager_assignment_id": true, "work_email": true,
	}
	for k := range pm {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q (over-collection guard P0-491-3)", k)
		}
	}
}

func TestBuild_StableOptionalFieldsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	w := sampleWorker()
	w.StartDate = time.Time{}
	w.EndDate = time.Time{}
	w.Title = ""
	w.Department = ""
	w.ManagerAssignmentID = ""
	w.WorkEmail = ""
	rec, _ := Build(w, "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
	pm := rec.GetPayload().AsMap()
	for _, k := range []string{"start_date", "end_date", "title", "department", "manager_assignment_id", "work_email"} {
		if _, ok := pm[k]; ok {
			t.Errorf("empty optional %q should be omitted, not emitted", k)
		}
	}
}

func TestBuild_TerminationDateEmittedAsISODate(t *testing.T) {
	t.Parallel()
	w := sampleWorker()
	w.Status = worker.StatusTerminated
	w.EndDate = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	rec, _ := Build(w, "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
	pm := rec.GetPayload().AsMap()
	if pm["end_date"] != "2026-05-31" {
		t.Errorf("end_date = %v; want 2026-05-31 (ISO date)", pm["end_date"])
	}
	if pm["start_date"] != "2024-01-15" {
		t.Errorf("start_date = %v; want 2024-01-15", pm["start_date"])
	}
}

func TestBuild_SameWorkerSameHourSameKey(t *testing.T) {
	t.Parallel()
	r1, _ := Build(sampleWorker(), "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
	r2, _ := Build(sampleWorker(), "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
	if r1.GetIdempotencyKey() != r2.GetIdempotencyKey() {
		t.Error("same worker + hour should yield same idempotency key")
	}
}

func TestBuild_ResultIsInconclusive(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleWorker(), "scf:IAC-22", "connector:rippling:workers@dev", "rippling", "prod")
	if rec.GetResult().String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want RESULT_INCONCLUSIVE (evaluator decides)", rec.GetResult())
	}
}
