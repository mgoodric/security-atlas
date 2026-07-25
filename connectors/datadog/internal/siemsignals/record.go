package siemsignals

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// EvidenceKind is the slice-636 Cloud-SIEM signal-history kind. It is a SEPARATE
// kind from datadog.siem_rule.v1 (slice 533) because a signal (a fired instance
// + a triage state + a triager + timeline) is structurally distinct from a
// rule's configuration. This mirrors the slice-488 -> 533 sibling split.
const EvidenceKind = "datadog.siem_signal.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// idemKey derives the idempotency key for one signal. The signal_id + the
// UTC-hour-truncated observed_at uniquely identify the record within the hour,
// collapsing same-signal re-runs into one ledger row.
func idemKey(signalID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("datadog.siem_signal|" + signalID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// Build turns a normalized signal into a pushable EvidenceRecord. actorID is the
// connector's `connector:datadog:siemsignals@<version>` attribution; controlID
// is the SCF control to attach; service/env scope the record. The Result is
// always INCONCLUSIVE: the connector reports descriptive triage METADATA (did
// the rule fire, what is the triage status, when, by whom); the platform
// evaluator owns the pass/fail call per (control, scope).
//
// There is no code path here that could place a signal message, a matched
// sample, a detection query, a body tag, or PII into the payload — the input
// Signal has no such field (P0-636).
func Build(sig Signal, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := sig.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"signal_id": sig.SignalID,
		"rule_id":   sig.RuleID,
		"severity":  sig.Severity,
		"status":    sig.Status,
	}
	if sig.RuleName != "" {
		pm["rule_name"] = sig.RuleName
	}
	if sig.TriagerHandle != "" {
		pm["triager_handle"] = sig.TriagerHandle
	}
	if !sig.FirstSeenAt.IsZero() {
		pm["first_seen_at"] = sig.FirstSeenAt.UTC().Format(time.RFC3339)
	}
	if !sig.TriagedAt.IsZero() {
		pm["triaged_at"] = sig.TriagedAt.UTC().Format(time.RFC3339)
	}
	if !sig.LastUpdatedAt.IsZero() {
		pm["last_updated_at"] = sig.LastUpdatedAt.UTC().Format(time.RFC3339)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey(sig.SignalID, now),
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
