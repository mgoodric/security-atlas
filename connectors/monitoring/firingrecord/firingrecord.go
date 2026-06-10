// Package firingrecord builds the canonical monitoring.alert_firing.v1 evidence
// record from a normalized firing.Firing. Both monitoring connectors (Datadog +
// Grafana, slice 535) share this builder because they share the evidence kind —
// the firing-history sibling of slice 488's shared monrecord builder.
//
// The builder is the single choke point that turns connector-side firing events
// into a pushed record: it sets the idempotency key (from the shared idem
// package, keyed on (vendor, rule_id, fired_at) so overlapping-window re-reads
// do not double-write), the evidence kind / schema version, the scope
// dimensions, the source attribution, and the body-free payload. There is no
// code path here that could place an alert message body, a triggering metric
// value, a secret webhook URL, or recipient PII into the payload — the input
// firing.Firing has no such field (P0-535).
package firingrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
	"github.com/mgoodric/security-atlas/connectors/monitoring/idem"
)

// EvidenceKind is the shared firing-history kind both monitoring connectors
// emit. It is a SEPARATE kind from monitoring.alert_config.v1 (slice 488)
// because a firing event (a timestamped fired/resolved transition + routing
// target) is structurally distinct from a rule's configuration.
const EvidenceKind = "monitoring.alert_firing.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns a normalized firing event into a pushable EvidenceRecord. actorID
// is the connector's `connector:<vendor>:firing@<version>` attribution;
// controlID is the SCF control to attach; service/env scope the record. The
// Result is always INCONCLUSIVE: the connector reports descriptive firing
// METADATA (a rule fired at T, resolved at T', routed to handle H); the
// platform evaluator owns the pass/fail call per (control, scope).
//
// There is no code path here that could place an alert message, a metric value,
// a secret URL, or PII into the payload — the input Firing has no such field
// (P0-535).
func Build(f firing.Firing, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := f.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"source_vendor": string(f.SourceVendor),
		"rule_id":       f.RuleID,
		"state":         f.State,
		"fired_at":      f.FiredAt.UTC().Format(time.RFC3339),
	}
	if !f.ResolvedAt.IsZero() {
		pm["resolved_at"] = f.ResolvedAt.UTC().Format(time.RFC3339)
	}
	if f.Target != nil {
		pm["target_kind"] = f.Target.Kind
		pm["target_name"] = f.Target.Name
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.AlertFiringKey(string(f.SourceVendor), f.RuleID, f.FiredAt, now),
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
