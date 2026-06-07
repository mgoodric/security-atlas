// Package alertcfg is the shared normalization layer for the monitoring
// connector family (Datadog + Grafana, slice 488). Both vendors' alert /
// monitor configuration reduces to the same shape — a rule with a name, a type,
// an enabled state, and a list of notification targets identified by NAME /
// HANDLE only — so they share one evidence kind (monitoring.alert_config.v1)
// and one normalizer.
//
// The load-bearing guard (P0-488-3): a Rule carries CONFIGURATION + routing
// TARGET-NAME metadata only. It deliberately has no field for a secret webhook
// URL, an integration token, a recipient email address (PII), dashboard JSON,
// metric time-series, or log results. The type system itself is the first line
// of the over-collection defence: there is nowhere to put a secret. Normalize
// additionally trims any target whose name is missing, and the cmd-layer test
// asserts no banned substring reaches an emitted record.
package alertcfg

import (
	"sort"
	"strings"
	"time"
)

// Vendor identifies the monitoring source. Maps 1:1 to the schema's
// source_vendor enum.
type Vendor string

const (
	// VendorDatadog is the Datadog monitoring connector.
	VendorDatadog Vendor = "datadog"
	// VendorGrafana is the Grafana monitoring connector.
	VendorGrafana Vendor = "grafana"
)

// Target is one notification routing target — NAME / HANDLE only. There is
// intentionally no URL / token / secret field on this struct (P0-488-3).
type Target struct {
	// Kind is the target type: "slack", "pagerduty", "email", "webhook",
	// "contact_point", etc. The KIND, never the secret.
	Kind string
	// Name is the handle / channel name / contact-point name. NEVER the secret
	// webhook URL, the integration token, or a recipient email address.
	Name string
}

// RawRule is the narrow, secret-free view a vendor client returns for one
// monitor / alert rule. The vendor clients map their API response into this
// shape, discarding every secret-bearing or telemetry field at the decode
// boundary. Tests construct it directly.
type RawRule struct {
	// ID is the vendor-native stable identifier (opaque, non-secret).
	ID string
	// Name is the human-readable rule / monitor name.
	Name string
	// Type is the vendor-native rule type (descriptive string).
	Type string
	// Enabled is whether the rule is currently active.
	Enabled bool
	// Folder is an optional organizational grouping (Grafana folder; empty for
	// Datadog).
	Folder string
	// Targets are the notification targets — names / handles only.
	Targets []Target
}

// Rule is the normalized record the cmd layer turns into an evidence record.
// Field names map 1:1 to the monitoring.alert_config.v1 schema.
type Rule struct {
	SourceVendor Vendor
	RuleID       string
	RuleName     string
	RuleType     string
	Enabled      bool
	Folder       string
	Targets      []Target
	ObservedAt   time.Time
}

// Normalize converts a vendor's raw rules into normalized Rules, stamping the
// vendor + a single observed-at. now is injectable for deterministic tests
// (nil -> time.Now UTC). Rules missing an id, a name, or a type are dropped
// (the schema requires them) rather than emitting an invalid record. Targets
// missing a name are dropped (a nameless target carries no evidence value and
// could only have come from a malformed source).
func Normalize(vendor Vendor, raw []RawRule, now func() time.Time) []Rule {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)
	out := make([]Rule, 0, len(raw))
	for _, r := range raw {
		name := strings.TrimSpace(r.Name)
		id := strings.TrimSpace(r.ID)
		typ := strings.TrimSpace(r.Type)
		if id == "" || name == "" || typ == "" {
			continue
		}
		out = append(out, Rule{
			SourceVendor: vendor,
			RuleID:       id,
			RuleName:     name,
			RuleType:     typ,
			Enabled:      r.Enabled,
			Folder:       strings.TrimSpace(r.Folder),
			Targets:      sanitizeTargets(r.Targets),
			ObservedAt:   observedAt,
		})
	}
	return out
}

// sanitizeTargets drops targets without a name and sorts the result for
// deterministic record shaping. It also trims whitespace. It does NOT inspect
// the value for secrets — secrets are excluded by construction upstream (the
// vendor clients never populate a secret into Target.Name); this is the
// belt-and-braces shaping pass.
func sanitizeTargets(in []Target) []Target {
	if len(in) == 0 {
		return nil
	}
	out := make([]Target, 0, len(in))
	for _, t := range in {
		name := strings.TrimSpace(t.Name)
		kind := strings.TrimSpace(t.Kind)
		if name == "" {
			continue
		}
		if kind == "" {
			kind = "unknown"
		}
		out = append(out, Target{Kind: kind, Name: name})
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
