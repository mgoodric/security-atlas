// Package monitors pulls Datadog monitor / alert inventory via the read-only
// Datadog API (GET /api/v1/monitor, requires the monitors_read scope).
//
// The load-bearing guard (P0-488-3 / threat-model I): the client decodes ONLY
// each monitor's id, name, type, and overall_state/options.enabled — plus the
// notification HANDLES parsed out of the monitor message. It NEVER materializes
// or emits the secret webhook URL behind an integration, an integration token,
// a recipient email address (PII), the monitor query (a metric expression that
// can embed sensitive tag values), dashboard JSON, or metric time-series.
//
// Datadog routes notifications via "@handle" mentions embedded in the monitor
// message (e.g. "@slack-sec-oncall @pagerduty-primary"). The client extracts
// those handles and classifies their kind from the handle prefix. It
// deliberately DROPS any "@user@example.com" email-recipient mention: a raw
// recipient email is PII and must never enter an evidence record.
package monitors

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
)

// API is the narrow surface Pull depends on. The concrete implementation issues
// read-only Datadog API calls; tests pass a fake. v0 reads the first bounded
// page; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListMonitors(ctx context.Context) ([]RawMonitor, error)
}

// RawMonitor is the narrow, secret-free view the Datadog client returns for one
// monitor. The HTTP client maps the Datadog API response into this shape,
// discarding the query, the options blob, and every secret/telemetry field at
// the decode boundary. Tests construct it directly.
type RawMonitor struct {
	ID      string
	Name    string
	Type    string
	Enabled bool
	// Message is the monitor message, used ONLY to parse "@handle" notification
	// mentions. The message body itself is not emitted.
	Message string
}

// handleRe matches Datadog "@handle" notification mentions. A handle is the
// run of non-whitespace characters after the @. Email recipients
// ("@user@example.com" — contains a second @) are filtered out separately.
var handleRe = regexp.MustCompile(`@([A-Za-z0-9._\-]+(?:@[A-Za-z0-9._\-]+)?)`)

// Collect lists every visible monitor and returns secret-free RawRules ready
// for alertcfg.Normalize. Separated from normalization so the cmd layer owns
// the observed-at clock.
func Collect(ctx context.Context, api API) ([]alertcfg.RawRule, error) {
	if api == nil {
		return nil, errors.New("monitors: API is nil")
	}
	raw, err := api.ListMonitors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list datadog monitors: %w", err)
	}
	out := make([]alertcfg.RawRule, 0, len(raw))
	for _, m := range raw {
		if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Name) == "" {
			continue
		}
		out = append(out, alertcfg.RawRule{
			ID:      m.ID,
			Name:    m.Name,
			Type:    normalizeType(m.Type),
			Enabled: m.Enabled,
			Targets: parseTargets(m.Message),
		})
	}
	return out, nil
}

// parseTargets extracts "@handle" notification mentions from the monitor
// message and classifies each handle's kind. It DROPS email-recipient mentions
// (handles containing an "@", i.e. "@user@example.com") so recipient PII never
// enters an evidence record (P0-488-3).
func parseTargets(message string) []alertcfg.Target {
	if message == "" {
		return nil
	}
	matches := handleRe.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]alertcfg.Target, 0, len(matches))
	for _, m := range matches {
		handle := m[1]
		// Drop email recipients: "@user@example.com" is PII, not a routing
		// handle. A handle that itself contains an "@" is an email address.
		if strings.Contains(handle, "@") {
			continue
		}
		if seen[handle] {
			continue
		}
		seen[handle] = true
		out = append(out, alertcfg.Target{Kind: classifyHandle(handle), Name: handle})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// classifyHandle maps a Datadog notification handle prefix to a target kind.
// Datadog integration handles are prefixed by the integration name
// (e.g. "slack-...", "pagerduty-...", "webhook-...", "teams-...", "opsgenie-...").
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

func normalizeType(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return "monitor"
	}
	return t
}
