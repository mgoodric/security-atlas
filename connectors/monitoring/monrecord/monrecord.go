// Package monrecord builds the canonical monitoring.alert_config.v1 evidence
// record from a normalized alertcfg.Rule. Both monitoring connectors (Datadog +
// Grafana, slice 488) share this builder because they share the evidence kind.
//
// The builder is the single choke point that turns connector-side config into a
// pushed record: it sets the idempotency key (from the shared idem package), the
// evidence kind / schema version, the scope dimensions, the source attribution,
// and the secret-free payload. There is no code path here that could place a
// secret into the payload — the input alertcfg.Rule has no secret field
// (P0-488-3).
package monrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
	"github.com/mgoodric/security-atlas/connectors/monitoring/idem"
)

// EvidenceKind is the shared kind both monitoring connectors emit.
const EvidenceKind = "monitoring.alert_config.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns a normalized rule into a pushable EvidenceRecord. actorID is the
// connector's `connector:<vendor>:<service>@<version>` attribution; controlID is
// the SCF control to attach; service/env scope the record. The Result is always
// INCONCLUSIVE: the connector reports descriptive configuration (enabled state +
// routing), and the platform evaluator owns the pass/fail call per (control,
// scope).
func Build(rule alertcfg.Rule, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := rule.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"source_vendor": string(rule.SourceVendor),
		"rule_id":       rule.RuleID,
		"rule_name":     rule.RuleName,
		"rule_type":     rule.RuleType,
		"enabled":       rule.Enabled,
	}
	if rule.Folder != "" {
		pm["folder"] = rule.Folder
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
		IdempotencyKey: idem.AlertConfigKey(string(rule.SourceVendor), rule.RuleID, now),
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
