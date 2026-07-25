// Package hierarchyrecord builds the canonical hris.manager_hierarchy.v1
// evidence record from a hierarchy.Edge (slice 571). Both HRIS connectors
// (Rippling + BambooHR) share this builder because they share the evidence kind:
// the manager reporting tree is derived from the same roster, so its shape is
// vendor-independent.
//
// The builder is the single choke point that turns a hierarchy edge into a
// pushed record: it sets the idempotency key (from the shared idem package), the
// evidence kind / schema version, the scope dimensions, the source attribution,
// and the PII-bounded payload. The payload carries ONLY opaque assignment ids +
// derived hierarchy facts (depth, orphaned-report flag, cycle-member flag) —
// there is no code path here that could place a manager's (or any worker's)
// name, email, phone, address, or any personal-contact / sensitive PII into the
// payload, because the input hierarchy.Edge has no such field (the slice-491
// identity boundary, extended to the hierarchy surface).
package hierarchyrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/hris/hierarchy"
	"github.com/mgoodric/security-atlas/connectors/hris/idem"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// EvidenceKind is the shared kind both HRIS connectors emit for the reporting
// tree.
const EvidenceKind = "hris.manager_hierarchy.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns one hierarchy edge into a pushable EvidenceRecord. sourceHRIS
// stamps provenance; controlID is the SCF control to attach; actorID is the
// connector's `connector:<vendor>:<service>@<version>` attribution;
// service/environment scope the record; observedAt is the roster read's
// hour-truncated observation time (shared across the whole run so a tree's
// records carry one consistent timestamp). The Result is always INCONCLUSIVE:
// the connector reports the descriptive reporting-tree facts; the platform
// evaluator owns the access-review routing decision.
func Build(e hierarchy.Edge, sourceHRIS worker.HRIS, controlID, actorID, service, environment string, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	now := observedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"source_hris":          string(sourceHRIS),
		"worker_assignment_id": e.WorkerAssignmentID,
		"depth":                float64(e.Depth),
		"orphaned_report":      e.OrphanedReport,
		"cycle_member":         e.CycleMember,
	}
	// Stable optional field — present only when this worker has a manager edge, so
	// a tree root (empty manager) does not emit an empty-string manager id
	// (matches the slice-004 / slice-491 stable-optional convention).
	if e.ManagerAssignmentID != "" {
		pm["manager_assignment_id"] = e.ManagerAssignmentID
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.ManagerHierarchyKey(string(sourceHRIS), e.WorkerAssignmentID, now),
		EvidenceKind:   EvidenceKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides routing
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}
