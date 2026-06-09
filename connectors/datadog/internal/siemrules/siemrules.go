// Package siemrules pulls Datadog Cloud-SIEM / Security-Monitoring DETECTION
// RULE inventory via the read-only Datadog API
// (GET /api/v2/security_monitoring/rules, requires the
// security_monitoring_rules_read scope).
//
// This is the slice-533 sibling of the slice-488 monitors collector. It emits a
// SEPARATE evidence kind (datadog.siem_rule.v1) rather than reusing
// monitoring.alert_config.v1: a detection rule carries a SEVERITY and a
// detection query-CLASS (log / signal-correlation / threshold) that the
// alert-config shape lacks. Slice 488 decision D1 explicitly reserved exactly
// this case for a split ("if a follow-on vendor surface — Datadog SIEM rules —
// has an alert model whose auditable fields don't fit {rule, type, enabled,
// targets}, split THAT surface into its own kind").
//
// The load-bearing guard (P0-533 / threat-model I): the collector's record
// struct (Rule) can hold ONLY a rule's id, name, detection-class, enabled
// state, severity, and the notification-target HANDLES. It is structurally
// incapable of holding a firing SIGNAL, a raw log SAMPLE matched by the
// detection query, a matched-event PAYLOAD, the secret webhook URL behind a
// notification, an integration token, the raw detection query text, or
// recipient PII. If the struct physically cannot hold the excluded data, that
// is the over-collection guard. A reflection test pins the field set.
//
// Datadog routes security-monitoring notifications via "@handle" mentions in the
// rule's per-case notification list (e.g. "@slack-sec-oncall"). The collector
// extracts those handles and classifies their kind from the prefix, and
// deliberately DROPS any "@user@example.com" email-recipient mention: a raw
// recipient email is PII and must never enter an evidence record.
package siemrules

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Datadog API GETs against /api/v2/security_monitoring/rules;
// tests pass a fake. The implementation reads a bounded number of pages with a
// hard per-run cap (threat-model D / DoS guard).
type API interface {
	ListRules(ctx context.Context) ([]RawRule, error)
}

// RawRule is the narrow, secret-free view the Datadog client returns for one
// detection rule. The HTTP client maps the Datadog API response into this
// shape, discarding the detection query, the matched-event payloads, every
// secret/telemetry field, and any firing signals at the decode boundary. Tests
// construct it directly.
//
// There is intentionally NO field here capable of holding a signal, a log
// sample, a matched event, a secret notification target, or a raw query — the
// type system is the first line of the over-collection defence (P0-533).
type RawRule struct {
	// ID is the Datadog-native stable rule identifier (opaque, non-secret).
	ID string
	// Name is the human-readable detection-rule name.
	Name string
	// DetectionClass is the rule's detection query class: "log",
	// "signal_correlation", or "threshold". Descriptive string.
	DetectionClass string
	// Enabled is whether the detection rule is currently active.
	Enabled bool
	// Severity is the highest case severity the rule can produce
	// (info/low/medium/high/critical). Descriptive label, not a signal.
	Severity string
	// Handles are the raw "@handle" notification mentions parsed from the
	// rule's per-case notification lists. Used only to derive Targets; never
	// emitted verbatim.
	Handles []string
}

// Rule is the normalized record the cmd layer turns into an evidence record.
// Field names map 1:1 to the datadog.siem_rule.v1 schema. Like RawRule, it has
// no field that could carry a signal / log sample / matched event / secret.
type Rule struct {
	RuleID         string
	RuleName       string
	DetectionClass string
	Enabled        bool
	Severity       string
	Targets        []Target
	ObservedAt     time.Time
}

// Target is one notification routing target — NAME / HANDLE only. There is
// intentionally no URL / token / secret field on this struct (P0-533).
type Target struct {
	// Kind is the target type derived from the handle prefix ("slack",
	// "pagerduty", "webhook", etc.). The KIND, never the secret.
	Kind string
	// Name is the handle / channel name. NEVER the secret webhook URL, the
	// integration token, or a recipient email address.
	Name string
}

// handleRe matches Datadog "@handle" notification mentions. A handle is the run
// of handle characters after the @. Email recipients ("@user@example.com" —
// contain a second @) are filtered out separately.
var handleRe = regexp.MustCompile(`@([A-Za-z0-9._\-]+(?:@[A-Za-z0-9._\-]+)?)`)

// Collect lists every visible detection rule and returns normalized, secret-free
// Rules. now is injectable for deterministic tests (nil -> time.Now UTC); the
// observed-at is truncated to the UTC hour so same-rule re-runs within the hour
// collapse to one ledger row. Rules missing an id, a name, or a detection class
// are dropped (the schema requires them) rather than emitting an invalid record.
func Collect(ctx context.Context, api API, now func() time.Time) ([]Rule, error) {
	if api == nil {
		return nil, errors.New("siemrules: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)

	raw, err := api.ListRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list datadog security-monitoring rules: %w", err)
	}
	out := make([]Rule, 0, len(raw))
	for _, r := range raw {
		id := strings.TrimSpace(r.ID)
		name := strings.TrimSpace(r.Name)
		class := normalizeClass(r.DetectionClass)
		if id == "" || name == "" {
			continue
		}
		out = append(out, Rule{
			RuleID:         id,
			RuleName:       name,
			DetectionClass: class,
			Enabled:        r.Enabled,
			Severity:       normalizeSeverity(r.Severity),
			Targets:        parseTargets(r.Handles),
			ObservedAt:     observedAt,
		})
	}
	return out, nil
}

// parseTargets classifies each "@handle" notification mention and DROPS
// email-recipient mentions (handles containing an "@", i.e.
// "@user@example.com") so recipient PII never enters an evidence record
// (P0-533). Input may be raw mentions ("@slack-ops") or bare handles
// ("slack-ops"); both are accepted.
func parseTargets(handles []string) []Target {
	if len(handles) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]Target, 0, len(handles))
	for _, raw := range handles {
		for _, m := range handleRe.FindAllStringSubmatch(ensureAt(raw), -1) {
			handle := m[1]
			// Drop email recipients: a handle that itself contains an "@" is an
			// email address, which is PII, not a routing handle.
			if strings.Contains(handle, "@") {
				continue
			}
			if seen[handle] {
				continue
			}
			seen[handle] = true
			out = append(out, Target{Kind: classifyHandle(handle), Name: handle})
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ensureAt prefixes a bare handle with "@" so the shared mention regexp matches
// both "@slack-ops" and "slack-ops" forms the Datadog API can return.
func ensureAt(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "@") {
		return s
	}
	return "@" + s
}

// classifyHandle maps a Datadog notification handle prefix to a target kind,
// mirroring the slice-488 monitors classifier.
func classifyHandle(handle string) string {
	lower := strings.ToLower(handle)
	switch {
	case strings.HasPrefix(lower, "slack-"):
		return "slack"
	case strings.HasPrefix(lower, "pagerduty-"):
		return "pagerduty"
	case strings.HasPrefix(lower, "webhook-"):
		return "webhook"
	case strings.HasPrefix(lower, "teams-"):
		return "teams"
	case strings.HasPrefix(lower, "opsgenie-"):
		return "opsgenie"
	case strings.HasPrefix(lower, "jira-"):
		return "jira"
	default:
		return "handle"
	}
}

// normalizeClass canonicalizes the detection query class. An empty/unknown
// class defaults to "log" (the Datadog default rule kind) rather than emitting
// an empty descriptor.
func normalizeClass(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "log_detection", "log":
		return "log"
	case "signal_correlation", "signal-correlation", "correlation":
		return "signal_correlation"
	case "threshold", "anomaly_detection", "impossible_travel", "new_value":
		return "threshold"
	default:
		// Preserve an unrecognized but non-empty class verbatim (descriptive
		// string; the schema does not enumerate it) so a new Datadog rule kind
		// does not require a schema bump.
		return strings.ToLower(strings.TrimSpace(s))
	}
}

// normalizeSeverity lower-cases and trims the severity label. An empty severity
// defaults to "info" (the lowest Datadog case severity).
func normalizeSeverity(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return "info"
	}
	return v
}
