// Package firing is the shared vendor-neutral layer for the monitoring
// connector family's alert-FIRING history surface (Datadog + Grafana, slice
// 535). It is the firing-history SIBLING of the slice-488 alertcfg /
// monrecord config-inventory layer: slice 488 reads which alerts are
// CONFIGURED (monitoring.alert_config.v1 — SOC 2 CC7.2); this surface reads
// what actually FIRED and was resolved (monitoring.alert_firing.v1 — SOC 2
// CC7.3/CC7.4 "the entity evaluates security events and responds to identified
// incidents"). Both vendors' firing collectors normalize to the one Firing
// shape and share this one evidence kind + builder + idempotency key, exactly
// as the config surface shares alertcfg + monrecord.
//
// The load-bearing guard (P0-535 / threat-model I, Information Disclosure
// DOMINANT): a Firing carries ONLY the firing event's identity + timeline +
// routing-target HANDLE — rule_id, vendor, fired_at, resolved_at, state, and
// the NAME/HANDLE of the notification target the alert routed to. It is
// structurally INCAPABLE of holding the alert MESSAGE body, the triggering
// METRIC VALUES, the secret WEBHOOK URL, or recipient PII. If the struct
// physically cannot hold the excluded data, that is the over-collection guard;
// a reflection test (TestStructuralOverCollectionGuard) fails the build if a
// forbidden field is added, and each vendor collector's drop test feeds a
// source event carrying a message body + metric values + a secret webhook URL +
// recipient PII and proves none reaches a Firing.
//
// Profile: bounded PULL on the operator's schedule (the slice-636 precedent).
// Each run reads a bounded look-back window of firing events from the vendor's
// poll/search API and pushes one record per event via the single Push RPC
// (invariant #3). This is NOT continuous monitoring and NOT an event-driven
// receiver — neither vendor offers a first-class push this connector receives
// (Datadog's Events API is a search surface; Grafana's alert state-history is a
// query surface). The window is named honestly (--<vendor>-firing-lookback,
// default 24h). See docs/audit-log/535-monitoring-alert-firing-decisions.md D1.
package firing

import (
	"sort"
	"strings"
	"time"
)

// Vendor identifies the monitoring source. Maps 1:1 to the schema's
// source_vendor enum. Re-declared here (rather than imported from alertcfg) so
// the firing surface stays self-contained; the values match alertcfg's.
type Vendor string

const (
	// VendorDatadog is the Datadog monitoring connector.
	VendorDatadog Vendor = "datadog"
	// VendorGrafana is the Grafana monitoring connector.
	VendorGrafana Vendor = "grafana"
)

// State is the normalized firing lifecycle state. Both vendors' native states
// fold into this small audit vocabulary.
const (
	// StateAlerting / firing: the alert is currently in its triggered state.
	StateAlerting = "alerting"
	// StateResolved / recovered / OK: the alert returned to a healthy state.
	StateResolved = "resolved"
	// StateNoData: the alert has no data to evaluate (Grafana NoData / Datadog
	// no-data transition) — an audit-relevant state, not silently dropped.
	StateNoData = "no_data"
	// StatePending: the condition is met but the for-duration has not elapsed
	// (Grafana Pending). Recorded honestly rather than coerced to alerting.
	StatePending = "pending"
)

// Target is one notification routing target — NAME / HANDLE only. There is
// intentionally no URL / token / secret field on this struct (P0-535): the
// secret webhook URL behind a handle is never materialized.
type Target struct {
	// Kind is the target type: "slack", "pagerduty", "webhook", "contact_point",
	// etc. The KIND, never the secret.
	Kind string
	// Name is the handle / channel name / contact-point name. NEVER the secret
	// webhook URL, the integration token, or a recipient email address.
	Name string
}

// RawFiring is the narrow, body-free view a vendor client returns for one
// firing event. The vendor clients map their API response into this shape,
// discarding the alert message body, the triggering metric values, the secret
// webhook URL, and recipient PII at the decode boundary. Tests construct it
// directly.
//
// There is intentionally NO field here capable of holding an alert message, a
// metric value, a secret URL, or recipient PII — the type system is the first
// line of the over-collection defence (P0-535).
type RawFiring struct {
	// RuleID is the vendor-native id of the rule/monitor that fired (opaque,
	// non-secret). Ties back to the monitoring.alert_config.v1 inventory record.
	RuleID string
	// State is the vendor-native firing state (alerting/resolved/OK/no_data/
	// pending/...). Normalized by Collect via NormalizeState.
	State string
	// FiredAt / ResolvedAt are the timeline timestamps. ResolvedAt is the zero
	// time while the alert is still firing.
	FiredAt    time.Time
	ResolvedAt time.Time
	// TargetHandle is the OPAQUE handle/name of the notification target the alert
	// routed to (a Slack channel name, a contact-point name, a "@handle"). An
	// email-shaped value (recipient PII) is dropped by Collect, never emitted.
	TargetHandle string
	// TargetKind classifies the target ("slack"/"pagerduty"/"webhook"/...).
	TargetKind string
}

// Firing is the normalized record the cmd layer turns into an evidence record.
// Field names map 1:1 to the monitoring.alert_firing.v1 schema. Like RawFiring,
// it has no field that could carry a message, a metric value, a secret URL, or
// PII.
type Firing struct {
	SourceVendor Vendor
	RuleID       string
	State        string
	FiredAt      time.Time
	ResolvedAt   time.Time
	Target       *Target
	ObservedAt   time.Time
}

// Collect normalizes a vendor's raw firing events into body-free Firings,
// stamping the vendor + a single observed-at. now is injectable for
// deterministic tests (nil -> time.Now UTC). The observed-at is truncated to
// the UTC hour so same-event re-runs within the hour collapse to one ledger row
// alongside the (vendor, rule_id, fired_at) idempotency key. Events missing a
// rule id or a fired_at are dropped (the schema requires them) rather than
// emitting an invalid record. An email-shaped target handle (recipient PII) is
// dropped before the record is built.
func Collect(vendor Vendor, raw []RawFiring, now func() time.Time) []Firing {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)
	out := make([]Firing, 0, len(raw))
	for _, r := range raw {
		ruleID := strings.TrimSpace(r.RuleID)
		if ruleID == "" || r.FiredAt.IsZero() {
			continue
		}
		out = append(out, Firing{
			SourceVendor: vendor,
			RuleID:       ruleID,
			State:        NormalizeState(r.State),
			FiredAt:      r.FiredAt.UTC(),
			ResolvedAt:   r.ResolvedAt.UTC(),
			Target:       sanitizeTarget(r.TargetKind, r.TargetHandle),
			ObservedAt:   observedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RuleID != out[j].RuleID {
			return out[i].RuleID < out[j].RuleID
		}
		return out[i].FiredAt.Before(out[j].FiredAt)
	})
	return out
}

// sanitizeTarget returns a routing Target, dropping an email-shaped handle
// (recipient PII) and any empty handle. A handle that contains an "@" is treated
// as an email address and dropped — a raw recipient email must never enter an
// evidence record (P0-535, mirroring the slice-488 monitors PII drop). Returns
// nil when there is no safe handle to record.
func sanitizeTarget(kind, handle string) *Target {
	name := strings.TrimSpace(handle)
	if name == "" || strings.Contains(name, "@") {
		return nil
	}
	k := strings.TrimSpace(kind)
	if k == "" {
		k = "handle"
	}
	return &Target{Kind: k, Name: name}
}

// NormalizeState canonicalizes a vendor's native firing state into the audit
// vocabulary. Datadog monitor states (Alert/OK/No Data/Warn) and Grafana
// alert-instance states (Alerting/Normal/NoData/Pending/Error) both fold here.
// An unknown / empty state defaults to "alerting" — the conservative,
// audit-relevant case (a firing we cannot confidently classify as resolved),
// never silently dropped.
func NormalizeState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "alert", "alerting", "firing", "triggered", "error":
		return StateAlerting
	case "ok", "okay", "resolved", "normal", "recovered", "recovery":
		return StateResolved
	case "no data", "no_data", "nodata", "no-data":
		return StateNoData
	case "pending", "warn", "warning":
		return StatePending
	default:
		return StateAlerting
	}
}
