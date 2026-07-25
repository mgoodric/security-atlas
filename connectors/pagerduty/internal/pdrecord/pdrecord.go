// Package pdrecord builds the canonical PagerDuty evidence records — the
// pagerduty.oncall_coverage.v1 record from a normalized oncall.Policy, the
// pagerduty.incident_summary.v1 record from a normalized incidents.Incident,
// and the pagerduty.postmortem_summary.v1 record from a normalized
// postmortems.Postmortem.
//
// The builder is the single choke point that turns connector-side data into a
// pushed record: it derives the idempotency key, sets the evidence kind /
// schema version, the scope dimensions, the source attribution, and the
// PII-free payload. There is no code path here that could place an incident
// free-text body, a responder's personal contact detail, or a postmortem
// narrative / action-item title into a payload — the input types
// (oncall.Policy / incidents.Incident / postmortems.Postmortem) have no such
// field (P0-489-3 / P0-538).
package pdrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/metrics"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/postmortems"
)

// Evidence kinds + schema versions this connector emits.
const (
	OnCallKind     = "pagerduty.oncall_coverage.v1"
	IncidentKind   = "pagerduty.incident_summary.v1"
	PostmortemKind = "pagerduty.postmortem_summary.v1"
	MetricsKind    = "pagerduty.response_metrics.v1"
	SchemaVersion  = "1.0.0"
	sourceVendorPD = "pagerduty"
)

// BuildOnCall turns a normalized escalation policy into a pushable
// EvidenceRecord. actorID is the connector's
// `connector:pagerduty:<service>@<version>` attribution; controlID is the SCF
// control to attach; service/environment scope the record. Result is always
// INCONCLUSIVE: the connector reports descriptive coverage; the platform
// evaluator owns the pass/fail call per (control, scope).
func BuildOnCall(p oncall.Policy, controlID, actorID, service, environment string, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	now := observedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"escalation_policy_id":   p.ID,
		"escalation_policy_name": p.Name,
		"num_tiers":              p.NumTier,
		"covered":                p.Covered,
	}
	if len(p.Tiers) > 0 {
		tiers := make([]any, 0, len(p.Tiers))
		for _, t := range p.Tiers {
			oncalls := make([]any, 0, len(t.OnCall))
			for _, oc := range t.OnCall {
				oncalls = append(oncalls, map[string]any{
					"assignee_kind": oc.Kind,
					"assignee_id":   oc.ID,
					"assignee_name": oc.Name,
				})
			}
			tier := map[string]any{"level": t.Level}
			if len(oncalls) > 0 {
				tier["on_call"] = oncalls
			} else {
				tier["on_call"] = []any{}
			}
			tiers = append(tiers, tier)
		}
		pm["tiers"] = tiers
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: onCallKey(p.ID, now),
		EvidenceKind:   OnCallKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}

// BuildIncident turns a normalized incident summary into a pushable
// EvidenceRecord. The idempotency key collapses same-incident re-runs within
// the hour into one ledger row.
func BuildIncident(in incidents.Incident, controlID, actorID, service, environment string, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	now := observedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"incident_id": in.ID,
		"status":      in.Status,
		"urgency":     in.Urgency,
		"created_at":  in.CreatedAt.UTC().Format(time.RFC3339),
	}
	if in.Number > 0 {
		pm["incident_number"] = in.Number
	}
	if in.ServiceID != "" {
		pm["service_id"] = in.ServiceID
	}
	if in.ServiceName != "" {
		pm["service_name"] = in.ServiceName
	}
	if !in.ResolvedAt.IsZero() {
		pm["resolved_at"] = in.ResolvedAt.UTC().Format(time.RFC3339)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: incidentKey(in.ID, now),
		EvidenceKind:   IncidentKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}

// BuildPostmortem turns a normalized postmortem-metadata view into a pushable
// EvidenceRecord. The payload is META-ONLY: the postmortem id, the linked
// incident id, the review status, the created / published timestamps, and the
// corrective-action ROLLUP (count + completed/open split). There is NO code
// path here that could place the postmortem narrative, timeline, root-cause
// prose, or an action-item title into the payload — the input type
// (postmortems.Postmortem) has no such field BY CONSTRUCTION (P0-538). The
// idempotency key collapses same-postmortem re-runs within the hour into one
// ledger row.
func BuildPostmortem(p postmortems.Postmortem, controlID, actorID, service, environment string, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	now := observedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"postmortem_id":          p.ID,
		"incident_id":            p.IncidentID,
		"status":                 p.Status,
		"created_at":             p.CreatedAt.UTC().Format(time.RFC3339),
		"action_item_count":      p.ActionItemCount,
		"action_items_completed": p.ActionItemsDone,
		"action_items_open":      p.ActionItemsOpen,
	}
	if !p.PublishedAt.IsZero() {
		pm["published_at"] = p.PublishedAt.UTC().Format(time.RFC3339)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: postmortemKey(p.ID, now),
		EvidenceKind:   PostmortemKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}

// BuildResponseMetrics turns a SERVICE-level incident-response aggregate into a
// pushable EvidenceRecord. The payload is an AGGREGATE-ONLY rollup: the opaque
// service id (the grain), the incident / acknowledged / resolved counts, and the
// MTTA / MTTR mean + p50 / p90 / p95 (whole seconds). There is NO code path here
// that could place a responder identity (name / email / id / contact) into the
// payload — the input type (metrics.ServiceMetrics) has no such field BY
// CONSTRUCTION (P0-539). The aggregation grain is the service, never a named
// responder. The idempotency key collapses same-service re-runs within the hour
// into one ledger row. windowSince/windowUntil bound the aggregation window and
// are emitted so an auditor knows the period.
func BuildResponseMetrics(m metrics.ServiceMetrics, controlID, actorID, service, environment string, windowSince, windowUntil, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	now := observedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"service_id":         m.ServiceID,
		"window_start":       windowSince.UTC().Format(time.RFC3339),
		"window_end":         windowUntil.UTC().Format(time.RFC3339),
		"incident_count":     m.IncidentCount,
		"acknowledged_count": m.AcknowledgedCount,
		"resolved_count":     m.ResolvedCount,
		"mtta_seconds": map[string]any{
			"mean": m.MTTASecondsMean,
			"p50":  m.MTTASecondsP50,
			"p90":  m.MTTASecondsP90,
			"p95":  m.MTTASecondsP95,
		},
		"mttr_seconds": map[string]any{
			"mean": m.MTTRSecondsMean,
			"p50":  m.MTTRSecondsP50,
			"p90":  m.MTTRSecondsP90,
			"p95":  m.MTTRSecondsP95,
		},
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: metricsKey(m.ServiceID, now),
		EvidenceKind:   MetricsKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}

// onCallKey: sha256("pagerduty.oncall_coverage|<policy_id>|<hour>").
func onCallKey(policyID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("pagerduty.oncall_coverage|" + policyID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// incidentKey: sha256("pagerduty.incident_summary|<incident_id>|<hour>").
func incidentKey(incidentID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("pagerduty.incident_summary|" + incidentID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// postmortemKey: sha256("pagerduty.postmortem_summary|<postmortem_id>|<hour>").
func postmortemKey(postmortemID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("pagerduty.postmortem_summary|" + postmortemID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// metricsKey: sha256("pagerduty.response_metrics|<service_id>|<hour>"). The
// grain is the service id (never a responder), so same-service re-runs within
// the hour collapse into one ledger row.
func metricsKey(serviceID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("pagerduty.response_metrics|" + serviceID + "|" + hour))
	return hex.EncodeToString(sum[:])
}
