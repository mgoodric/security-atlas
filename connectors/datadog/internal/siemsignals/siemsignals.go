// Package siemsignals pulls Datadog Cloud-SIEM / Security-Monitoring SIGNAL
// HISTORY — the triage-outcome record of detection rules that actually FIRED —
// via the read-only Datadog security-signals search API
// (GET /api/v2/security_monitoring/signals, requires the
// security_monitoring_signals_read scope).
//
// This is the slice-636 sibling of slice-533's datadog.siem_rule.v1. Slice 533
// reads detection-rule CONFIGURATION (SOC 2 CC7.2 — which rules exist). This
// surface reads what FIRED and how it was TRIAGED (SOC 2 CC7.3 — incident
// response): "show that rules actually fired and were triaged over the audit
// period, when, and by whom." It emits a SEPARATE evidence kind
// (datadog.siem_signal.v1): a signal is a fired instance + a triage state + a
// triager, structurally distinct from a rule's configuration.
//
// Profile: bounded PULL on the operator's schedule (same cadence as slice 533).
// Each run reads a bounded look-back window of signals and pushes one record per
// signal via the single Push RPC (invariant #3). This is NOT continuous
// monitoring and NOT an event-driven receiver — Datadog's security-signals API
// is a search/poll surface and offers no first-class push this connector
// receives. The window is named honestly (--lookback, default 24h).
//
// The load-bearing guard (P0-636 / the slice-533 structural template): the
// collector's RawSignal + Signal structs can hold ONLY a signal's id, the
// firing rule's id + name, severity, the triage status, the timeline
// timestamps, and the OPAQUE triager handle. They are structurally INCAPABLE of
// holding the signal MESSAGE body, the matched log/event SAMPLES, the raw
// detection QUERY, signal-body tags/facets, or any recipient/actor PII (a raw
// email). If the struct physically cannot hold the excluded data, that is the
// over-collection guard. A reflection test pins the field set; a normalize test
// feeds a message/samples/query/email fixture and proves none reaches a record.
package siemsignals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Datadog API GETs against
// /api/v2/security_monitoring/signals; tests pass a fake. The implementation
// reads a bounded number of pages with a hard per-run cap (DoS guard) over a
// bounded look-back window.
type API interface {
	// ListSignals returns the signals observed in [since, now]. The bounded
	// window keeps the read honest and small (NOT "all history").
	ListSignals(ctx context.Context, since time.Time) ([]RawSignal, error)
}

// RawSignal is the narrow, body-free view the Datadog client returns for one
// security signal. The HTTP client maps the Datadog API response into this
// shape, discarding the signal message, matched samples, the detection query,
// signal-body tags/facets, and any PII at the decode boundary. Tests construct
// it directly.
//
// There is intentionally NO field here capable of holding a signal message, a
// matched log/event sample, a detection query, a body tag, or a raw email —
// the type system is the first line of the over-collection defence (P0-636).
type RawSignal struct {
	// ID is the Datadog-native stable signal identifier (opaque, non-secret).
	ID string
	// RuleID is the firing detection rule's id (ties back to the slice-533
	// datadog.siem_rule.v1 configuration record).
	RuleID string
	// RuleName is the firing rule's human-readable name (descriptive label).
	RuleName string
	// Severity is the signal's severity label (info/low/medium/high/critical).
	Severity string
	// Status is the raw Datadog triage_state (open/under_review/archived) or an
	// audit alias (triaged/closed). Normalized by Collect.
	Status string
	// FirstSeen / Triaged / LastUpdated are the timeline timestamps proving a
	// triage happened within the audit period. Zero when the source omits one.
	FirstSeen   time.Time
	Triaged     time.Time
	LastUpdated time.Time
	// TriagerHandle is the OPAQUE handle/id of who triaged. An email-shaped
	// value is dropped by Collect (PII), never emitted.
	TriagerHandle string
}

// Signal is the normalized record the cmd layer turns into an evidence record.
// Field names map 1:1 to the datadog.siem_signal.v1 schema. Like RawSignal, it
// has no field that could carry a message, sample, query, body tag, or PII.
type Signal struct {
	SignalID      string
	RuleID        string
	RuleName      string
	Severity      string
	Status        string
	FirstSeenAt   time.Time
	TriagedAt     time.Time
	LastUpdatedAt time.Time
	TriagerHandle string
	ObservedAt    time.Time
}

// Collect lists every signal in the bounded look-back window and returns
// normalized, body-free Signals. now is injectable for deterministic tests (nil
// -> time.Now UTC); lookback is the bounded window (<=0 defaults to 24h). The
// observed-at is truncated to the UTC hour so same-signal re-runs within the
// hour collapse to one ledger row. Signals missing an id or a rule id are
// dropped (the schema requires them) rather than emitting an invalid record.
func Collect(ctx context.Context, api API, lookback time.Duration, now func() time.Time) ([]Signal, error) {
	if api == nil {
		return nil, errors.New("siemsignals: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	nowUTC := now().UTC()
	observedAt := nowUTC.Truncate(time.Hour)
	since := nowUTC.Add(-lookback)

	raw, err := api.ListSignals(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("list datadog security-signals: %w", err)
	}
	out := make([]Signal, 0, len(raw))
	for _, s := range raw {
		id := strings.TrimSpace(s.ID)
		ruleID := strings.TrimSpace(s.RuleID)
		if id == "" || ruleID == "" {
			continue
		}
		out = append(out, Signal{
			SignalID:      id,
			RuleID:        ruleID,
			RuleName:      strings.TrimSpace(s.RuleName),
			Severity:      normalizeSeverity(s.Severity),
			Status:        normalizeStatus(s.Status),
			FirstSeenAt:   s.FirstSeen.UTC(),
			TriagedAt:     s.Triaged.UTC(),
			LastUpdatedAt: s.LastUpdated.UTC(),
			TriagerHandle: sanitizeHandle(s.TriagerHandle),
			ObservedAt:    observedAt,
		})
	}
	return out, nil
}

// sanitizeHandle drops an email-shaped triager value (PII) and trims the rest.
// A handle that contains an "@" is treated as an email address and dropped — a
// raw triager email must never enter an evidence record (P0-636, mirroring the
// slice-533 recipient-email PII drop).
func sanitizeHandle(s string) string {
	v := strings.TrimSpace(s)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "@") {
		return ""
	}
	return v
}

// normalizeStatus canonicalizes the Datadog triage_state into the audit
// vocabulary. Datadog emits open/under_review/archived; we also accept the
// triaged/closed aliases for forward-compat. An empty/unknown status defaults
// to "open" — the conservative, audit-relevant case (an un-triaged signal),
// never silently dropped.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "open", "new":
		return "open"
	case "under_review", "under-review", "in_review", "reviewing":
		return "under_review"
	case "archived", "closed", "resolved", "triaged":
		return normalizeTerminal(s)
	default:
		return "open"
	}
}

// normalizeTerminal maps the terminal triage states to the two audit-vocabulary
// terminal values: "archived" (Datadog's native closed state) and "triaged"
// (an explicit triage disposition that is not yet archived).
func normalizeTerminal(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "triaged") {
		return "triaged"
	}
	return "archived"
}

// normalizeSeverity lower-cases and trims the severity label. An empty severity
// defaults to "info" (the lowest Datadog severity).
func normalizeSeverity(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return "info"
	}
	return v
}
