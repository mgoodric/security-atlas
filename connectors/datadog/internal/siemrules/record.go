package siemrules

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// EvidenceKind is the sibling detection-rule kind (slice 533). It is a SEPARATE
// kind from monitoring.alert_config.v1 (slice 488) because a detection rule
// carries a severity + a detection-class field the alert-config shape lacks —
// the split slice-488 D1 reserved.
const EvidenceKind = "datadog.siem_rule.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// idemKey derives the idempotency key for one detection rule. The rule_id +
// the UTC-hour-truncated observed_at uniquely identify the record within the
// hour, collapsing same-rule re-runs into one ledger row — the same shape as
// the monitoring connector family's idem package, scoped to this kind.
func idemKey(ruleID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("datadog.siem_rule|" + ruleID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// Build turns a normalized detection rule into a pushable EvidenceRecord.
// actorID is the connector's `connector:datadog:siemrules@<version>`
// attribution; controlID is the SCF control to attach; service/env scope the
// record. The Result is always INCONCLUSIVE: the connector reports descriptive
// configuration (detection class + enabled + severity + routing); the platform
// evaluator owns the pass/fail call per (control, scope).
//
// There is no code path here that could place a signal, a log sample, a matched
// event, a secret, or a raw query into the payload — the input Rule has no such
// field (P0-533).
func Build(rule Rule, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := rule.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"rule_id":         rule.RuleID,
		"rule_name":       rule.RuleName,
		"detection_class": rule.DetectionClass,
		"enabled":         rule.Enabled,
		"severity":        rule.Severity,
	}
	if len(rule.Targets) > 0 {
		targets := make([]any, 0, len(rule.Targets))
		for _, t := range rule.Targets {
			targets = append(targets, map[string]any{
				"target_kind": t.Kind,
				"target_name": t.Name,
			})
		}
		pm["notification_targets"] = targets
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey(rule.RuleID, now),
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
