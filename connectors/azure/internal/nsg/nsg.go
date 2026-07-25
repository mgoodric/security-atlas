// Package nsg inspects Azure Network Security Group (NSG) / firewall rule
// posture — the load-bearing signal for the Azure connector's NSG evidence kind
// (slice 520).
//
// Source: read-only Azure Resource Manager (ARM Reader role) — the SAME role
// the storage and AKS kinds use; no new Azure scope (P0-520-1). The connector
// reads NSG security-RULE configuration only — NEVER flow logs, packet
// captures, or traffic contents (P0-520-2), and NEVER mutates a network
// resource (read-only list — P0-520-3). The inbound/outbound security-rule set
// of each NSG is the minimum that demonstrates the network-segmentation control
// (e.g. "no NSG allows 0.0.0.0/0 inbound on 22/3389").
package nsg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConfigResult enumerates the per-NSG verdict. Maps 1:1 onto the gRPC Result
// enum.
type ConfigResult string

const (
	ResultPass         ConfigResult = "pass"
	ResultFail         ConfigResult = "fail"
	ResultInconclusive ConfigResult = "inconclusive"
)

// managementPorts are the ports whose unrestricted (Internet / 0.0.0.0/0)
// inbound exposure is the canonical network-segmentation failure the verdict
// flags (SSH + RDP). The evaluator owns the binding decision; this is a
// descriptive default.
var managementPorts = map[string]bool{"22": true, "3389": true}

// anySource enumerates the source-prefix tokens that mean "the whole Internet"
// for the verdict's open-management-port check.
var anySource = map[string]bool{"*": true, "0.0.0.0/0": true, "internet": true, "any": true}

// SecurityRule is one inbound/outbound security rule of an NSG. Field names map
// 1:1 to the azure.nsg_rules.v1 schema's rule items.
//
// Over-collection guard (P0-520-2, structural): this struct carries rule
// METADATA ONLY. There is deliberately NO field for flow logs, packet captures,
// traffic contents, or any payload byte — there is no place for such data to
// land even if a future ARM field exposed it. The RulesOnly test pins this.
type SecurityRule struct {
	Name                     string
	Direction                string // "inbound" | "outbound"
	Access                   string // "allow" | "deny"
	Protocol                 string // "tcp" | "udp" | "icmp" | "*"
	Priority                 int
	SourceAddressPrefix      string
	DestinationAddressPrefix string
	SourcePortRange          string
	DestinationPortRange     string
}

// GroupConfig is the per-NSG payload the connector emits. Field names map 1:1 to
// the azure.nsg_rules.v1 schema.
//
// Over-collection guard (P0-520-2, structural): management-plane RULE
// configuration ONLY. No flow-log, packet-capture, or traffic-content field
// exists. The RulesOnly test pins this.
type GroupConfig struct {
	NSGID             string
	NSGName           string
	SubscriptionID    string
	ResourceGroup     string
	Location          string
	Rules             []SecurityRule
	AssociatedSubnets int // count of subnet associations (summary, not topology)
	AssociatedNICs    int // count of network-interface associations (summary)
	Result            ConfigResult
	Reason            string // human-readable inconclusive / fail reason
	ObservedAt        time.Time
}

// RawGroup is the narrow view the API surface returns for one NSG. The concrete
// ARM client maps the SDK response into this shape; tests construct it directly.
//
// Over-collection guard: RULE configuration + association COUNTS only — no flow
// logs, no packet data, no traffic contents.
type RawGroup struct {
	ID                string
	Name              string
	ResourceGroup     string
	Location          string
	Rules             []SecurityRule
	AssociatedSubnets int
	AssociatedNICs    int
	// ReadError, when non-empty, marks the NSG as INCONCLUSIVE (a per-NSG ARM
	// read errored) rather than dropping it.
	ReadError string
}

// API is the narrow surface Inspect depends on. The concrete implementation
// wraps the read-only Azure Resource Manager network-security-groups list; tests
// pass a fake. v0 lists the first bounded page for one subscription; cursor
// pagination + multi-subscription enumeration are documented follow-ons
// (threat-model D, shared with slice 486 R3).
type API interface {
	ListNetworkSecurityGroups(ctx context.Context) ([]RawGroup, error)
}

// Inspect returns the security-rule posture for every visible NSG in the
// subscription. subscriptionID scopes every record. now is injectable for
// deterministic tests (nil -> time.Now UTC).
func Inspect(ctx context.Context, api API, subscriptionID string, now func() time.Time) ([]GroupConfig, error) {
	if api == nil {
		return nil, errors.New("nsg: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListNetworkSecurityGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list network security groups: %w", err)
	}
	observedAt := now()
	out := make([]GroupConfig, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" || r.Name == "" {
			continue
		}
		cfg := GroupConfig{
			NSGID:             r.ID,
			NSGName:           r.Name,
			SubscriptionID:    subscriptionID,
			ResourceGroup:     r.ResourceGroup,
			Location:          r.Location,
			Rules:             r.Rules,
			AssociatedSubnets: r.AssociatedSubnets,
			AssociatedNICs:    r.AssociatedNICs,
			ObservedAt:        observedAt,
		}
		cfg.Result, cfg.Reason = verdict(r)
		out = append(out, cfg)
	}
	return out, nil
}

// verdict deterministically scores the NSG's segmentation posture. FAIL when a
// rule allows inbound from the whole Internet to a management port (SSH/RDP);
// INCONCLUSIVE when the per-NSG read errored; PASS otherwise. The platform
// evaluator owns the final pass/fail per (control, scope) — this is a
// descriptive default.
func verdict(r RawGroup) (ConfigResult, string) {
	if r.ReadError != "" {
		return ResultInconclusive, "read nsg rules: " + r.ReadError
	}
	for _, rule := range r.Rules {
		if openManagementPort(rule) {
			return ResultFail, fmt.Sprintf("rule %q allows inbound from the Internet to management port %s",
				rule.Name, rule.DestinationPortRange)
		}
	}
	return ResultPass, ""
}

// openManagementPort reports whether a rule allows inbound traffic from the
// whole Internet to a management port (22/3389).
func openManagementPort(rule SecurityRule) bool {
	if !strings.EqualFold(rule.Direction, "inbound") || !strings.EqualFold(rule.Access, "allow") {
		return false
	}
	if !anySource[strings.ToLower(strings.TrimSpace(rule.SourceAddressPrefix))] {
		return false
	}
	return portRangeHitsManagement(rule.DestinationPortRange)
}

// portRangeHitsManagement reports whether an Azure destination-port-range token
// covers a management port. Handles a single port ("22"), a wildcard ("*"), and
// a hyphen range ("0-65535").
func portRangeHitsManagement(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if spec == "*" {
		return true
	}
	if managementPorts[spec] {
		return true
	}
	if lo, hi, ok := parseRange(spec); ok {
		for p := range managementPorts {
			if n, ok2 := atoi(p); ok2 && n >= lo && n <= hi {
				return true
			}
		}
	}
	return false
}

func parseRange(spec string) (lo, hi int, ok bool) {
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, okA := atoi(strings.TrimSpace(parts[0]))
	b, okB := atoi(strings.TrimSpace(parts[1]))
	if !okA || !okB || a > b {
		return 0, 0, false
	}
	return a, b, true
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
