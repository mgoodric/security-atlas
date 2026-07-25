// Package workerrecord builds the canonical hris.worker_lifecycle.v1 evidence
// record from a normalized worker.Worker. Both HRIS connectors (Rippling +
// BambooHR, slice 491) share this builder because they share the evidence kind.
//
// The builder is the single choke point that turns connector-side worker
// lifecycle into a pushed record: it sets the idempotency key (from the shared
// idem package), the evidence kind / schema version, the scope dimensions, the
// source attribution, and the PII-bounded payload. There is no code path here
// that could place SSN, compensation, home address, bank/payment, benefits,
// performance, date-of-birth, or personal-contact PII into the payload — the
// input worker.Worker has no such field (P0-491-3).
package workerrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/hris/idem"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// EvidenceKind is the shared kind both HRIS connectors emit.
const EvidenceKind = "hris.worker_lifecycle.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns a normalized worker into a pushable EvidenceRecord. actorID is the
// connector's `connector:<vendor>:<service>@<version>` attribution; controlID is
// the SCF control to attach; service/env scope the record. The Result is always
// INCONCLUSIVE: the connector reports the descriptive worker-lifecycle facts
// (employment status, dates, role, department); the platform evaluator owns the
// access-review pass/fail call per (control, scope) by reconciling the roster
// against the IdP/app entitlements.
func Build(w worker.Worker, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := w.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"source_hris":       string(w.SourceHRIS),
		"worker_id":         w.WorkerID,
		"employment_status": string(w.Status),
	}
	// Stable optional fields — present only when the source supplied them, so a
	// record's shape is stable for a given worker and a missing optional does not
	// emit an empty string / zero date (matches the slice-004 stable-optional
	// convention).
	if !w.StartDate.IsZero() {
		pm["start_date"] = w.StartDate.UTC().Format("2006-01-02")
	}
	if !w.EndDate.IsZero() {
		pm["end_date"] = w.EndDate.UTC().Format("2006-01-02")
	}
	if w.Title != "" {
		pm["title"] = w.Title
	}
	if w.Department != "" {
		pm["department"] = w.Department
	}
	if w.ManagerAssignmentID != "" {
		pm["manager_assignment_id"] = w.ManagerAssignmentID
	}
	if w.WorkEmail != "" {
		pm["work_email"] = w.WorkEmail
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.WorkerLifecycleKey(string(w.SourceHRIS), w.WorkerID, now),
		EvidenceKind:   EvidenceKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}
