// Package firewall inspects Azure Firewall rule-collection posture — the
// load-bearing signal for the Azure connector's Azure-Firewall evidence kind
// (slice 614).
//
// Source: read-only Azure Resource Manager (ARM Reader role) — the SAME role
// the storage, AKS, NSG and Key-Vault kinds use; no new Azure scope (P0-614-1).
// The connector reads firewall-policy rule-collection-group CONFIGURATION only
// — the network + application rule collections (and their rule-collection-group
// priority ordering) — NEVER flow logs, packet captures, traffic contents,
// NAT-rule secrets, threat-intel feeds, or route tables (P0-614-2), and NEVER
// mutates a network resource (read-only list — P0-614-3). Azure Firewall is a
// distinct boundary-protection control point from NSGs: a centralized,
// policy-based L3-L7 firewall rather than per-subnet/per-NIC NSG rules, so it
// answers the sibling network-segmentation question at the network-perimeter
// grain.
package firewall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConfigResult enumerates the per-firewall verdict. Maps 1:1 onto the gRPC
// Result enum.
type ConfigResult string

const (
	ResultPass         ConfigResult = "pass"
	ResultFail         ConfigResult = "fail"
	ResultInconclusive ConfigResult = "inconclusive"
)

// managementPorts are the destination ports whose unrestricted (Internet /
// 0.0.0.0/0 / *) inbound exposure is the canonical network-segmentation failure
// the verdict flags (SSH + RDP). The evaluator owns the binding decision; this
// is a descriptive default.
var managementPorts = map[string]bool{"22": true, "3389": true}

// anySource enumerates the source tokens that mean "the whole Internet" for the
// verdict's open-management-port check.
var anySource = map[string]bool{"*": true, "0.0.0.0/0": true, "internet": true, "any": true}

// Rule is one rule inside a rule collection. Field names map 1:1 to the
// azure.firewall_rules.v1 schema's rule items.
//
// Over-collection guard (P0-614-2, structural): this struct carries rule
// METADATA ONLY. There is deliberately NO field for flow logs, packet captures,
// traffic contents, NAT-rule secrets, threat-intel feeds, route tables, or any
// payload byte — there is no place for such data to land even if a future ARM
// field exposed it. The RuleConfigOnly test pins this.
type Rule struct {
	Name                 string
	Protocols            []string // network: tcp/udp/icmp/any; application: http/https/mssql
	SourceAddresses      []string // source address prefixes / tags
	DestinationAddresses []string // destination address prefixes (network rules)
	DestinationPorts     []string // destination port ranges (network rules)
	DestinationFQDNs     []string // target FQDNs (application rules)
}

// RuleCollection is one network or application rule collection inside a
// rule-collection group. Field names map 1:1 to the azure.firewall_rules.v1
// schema's rule-collection items.
//
// Over-collection guard (P0-614-2, structural): rule CONFIGURATION ONLY.
type RuleCollection struct {
	Name     string
	Type     string // "network" | "application"
	Action   string // "allow" | "deny"
	Priority int
	Rules    []Rule
}

// RuleCollectionGroup is one rule-collection group of a firewall policy. The
// group priority is the rule-collection-group ordering signal the slice spec
// names explicitly.
//
// Over-collection guard (P0-614-2, structural): CONFIGURATION ordering ONLY.
type RuleCollectionGroup struct {
	Name            string
	Priority        int
	RuleCollections []RuleCollection
}

// PolicyConfig is the per-firewall-policy payload the connector emits. Field
// names map 1:1 to the azure.firewall_rules.v1 schema.
//
// Over-collection guard (P0-614-2, structural): management-plane rule
// CONFIGURATION ONLY. No flow-log, packet-capture, traffic-content, NAT-secret,
// threat-intel, or route-table field exists. The RuleConfigOnly test pins this.
type PolicyConfig struct {
	PolicyID             string
	PolicyName           string
	SubscriptionID       string
	ResourceGroup        string
	Location             string
	RuleCollectionGroups []RuleCollectionGroup
	Result               ConfigResult
	Reason               string // human-readable inconclusive / fail reason
	ObservedAt           time.Time
}

// RawPolicy is the narrow view the API surface returns for one firewall policy.
// The concrete ARM client maps the SDK response into this shape; tests construct
// it directly.
//
// Over-collection guard: rule CONFIGURATION only — no flow logs, packet data,
// traffic contents, NAT secrets, threat-intel feeds, or route tables.
type RawPolicy struct {
	ID                   string
	Name                 string
	ResourceGroup        string
	Location             string
	RuleCollectionGroups []RuleCollectionGroup
	// ReadError, when non-empty, marks the policy as INCONCLUSIVE (a per-policy
	// ARM read errored) rather than dropping it.
	ReadError string
}

// API is the narrow surface Inspect depends on. The concrete implementation
// wraps the read-only Azure Resource Manager firewall-policies list plus a
// scoped read of each policy's rule-collection groups; tests pass a fake. v0
// lists the first bounded page for one subscription; cursor pagination +
// multi-subscription enumeration are documented follow-ons (threat-model D,
// shared with slice 486 R3).
type API interface {
	ListFirewallPolicies(ctx context.Context) ([]RawPolicy, error)
}

// Inspect returns the rule-collection posture for every visible Azure Firewall
// policy in the subscription. subscriptionID scopes every record. now is
// injectable for deterministic tests (nil -> time.Now UTC).
func Inspect(ctx context.Context, api API, subscriptionID string, now func() time.Time) ([]PolicyConfig, error) {
	if api == nil {
		return nil, errors.New("firewall: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListFirewallPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list firewall policies: %w", err)
	}
	observedAt := now()
	out := make([]PolicyConfig, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" || r.Name == "" {
			continue
		}
		cfg := PolicyConfig{
			PolicyID:             r.ID,
			PolicyName:           r.Name,
			SubscriptionID:       subscriptionID,
			ResourceGroup:        r.ResourceGroup,
			Location:             r.Location,
			RuleCollectionGroups: r.RuleCollectionGroups,
			ObservedAt:           observedAt,
		}
		cfg.Result, cfg.Reason = verdict(r)
		out = append(out, cfg)
	}
	return out, nil
}

// verdict deterministically scores the firewall policy's segmentation posture.
// FAIL when an allow rule permits traffic from the whole Internet to a
// management port (SSH/RDP); INCONCLUSIVE when the per-policy read errored; PASS
// otherwise. The platform evaluator owns the final pass/fail per (control,
// scope) — this is a descriptive default.
func verdict(r RawPolicy) (ConfigResult, string) {
	if r.ReadError != "" {
		return ResultInconclusive, "read firewall rule collections: " + r.ReadError
	}
	for _, g := range r.RuleCollectionGroups {
		for _, c := range g.RuleCollections {
			if !strings.EqualFold(c.Action, "allow") {
				continue
			}
			for _, rule := range c.Rules {
				if openManagementRule(rule) {
					return ResultFail, fmt.Sprintf("rule %q in collection %q allows traffic from the Internet to a management port",
						rule.Name, c.Name)
				}
			}
		}
	}
	return ResultPass, ""
}

// openManagementRule reports whether a network rule allows traffic from the
// whole Internet to a management port (22/3389). Application rules (FQDN-based,
// no port surface) cannot match the management-port heuristic and are skipped.
func openManagementRule(rule Rule) bool {
	if !hasAnySource(rule.SourceAddresses) {
		return false
	}
	for _, p := range rule.DestinationPorts {
		if portRangeHitsManagement(p) {
			return true
		}
	}
	return false
}

func hasAnySource(sources []string) bool {
	for _, s := range sources {
		if anySource[strings.ToLower(strings.TrimSpace(s))] {
			return true
		}
	}
	return false
}

// portRangeHitsManagement reports whether an Azure destination-port token covers
// a management port. Handles a single port ("22"), a wildcard ("*"), and a
// hyphen range ("0-65535").
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
