// Package oncall pulls PagerDuty escalation-policy + on-call coverage via the
// read-only PagerDuty REST API (GET /escalation_policies). It returns the
// COVERAGE FACTS needed to prove an incident-response capability exists and is
// staffed: each escalation policy, its tiers, and the on-call IDENTITY
// (assignee id + display name) at each tier.
//
// The load-bearing guard (P0-489-3 / threat-model I): the client decodes ONLY
// each policy's id/name, each escalation rule's target id/name/type, and the
// tier ordering. It NEVER materializes or emits a responder's personal phone
// number, personal email address, or any other personal contact detail, and
// never any incident free-text. json.Decode discards JSON keys with no matching
// struct field, so the unwanted PII fields never enter memory as connector
// data.
package oncall

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only PagerDuty API calls; tests pass a fake. v0 reads the first
// bounded page; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListEscalationPolicies(ctx context.Context) ([]RawPolicy, error)
}

// RawPolicy is the narrow, PII-free view the PagerDuty client returns for one
// escalation policy. The HTTP client maps the API response into this shape,
// discarding every personal-contact field at the decode boundary. Tests
// construct it directly.
type RawPolicy struct {
	ID    string
	Name  string
	Tiers []RawTier
}

// RawTier is one escalation rule (tier) with its on-call targets.
type RawTier struct {
	// Level is the 1-based tier position (1 = first responders).
	Level   int
	Targets []RawTarget
}

// RawTarget is an on-call assignee at a tier — identity only. Kind is "user"
// or "schedule"; Name is the display name. There is no contact-detail field
// here BY CONSTRUCTION: the type cannot carry a phone number or personal email.
type RawTarget struct {
	Kind string
	ID   string
	Name string
}

// Policy is the normalized coverage view for one escalation policy.
type Policy struct {
	ID      string
	Name    string
	NumTier int
	Covered bool
	Tiers   []Tier
}

// Tier is a normalized escalation tier.
type Tier struct {
	Level  int
	OnCall []OnCall
}

// OnCall is a normalized on-call assignee — identity only.
type OnCall struct {
	Kind string
	ID   string
	Name string
}

// Collect lists every escalation policy and returns secret/PII-free coverage
// records. Separated from record-building so the cmd layer owns the
// observed-at clock.
func Collect(ctx context.Context, api API) ([]Policy, error) {
	if api == nil {
		return nil, errors.New("oncall: API is nil")
	}
	raw, err := api.ListEscalationPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pagerduty escalation policies: %w", err)
	}
	out := make([]Policy, 0, len(raw))
	for _, p := range raw {
		id := strings.TrimSpace(p.ID)
		name := strings.TrimSpace(p.Name)
		if id == "" || name == "" {
			continue
		}
		tiers := make([]Tier, 0, len(p.Tiers))
		covered := false
		for _, t := range p.Tiers {
			oncall := make([]OnCall, 0, len(t.Targets))
			for _, tg := range t.Targets {
				tid := strings.TrimSpace(tg.ID)
				tname := strings.TrimSpace(tg.Name)
				if tid == "" || tname == "" {
					continue
				}
				oncall = append(oncall, OnCall{
					Kind: normalizeKind(tg.Kind),
					ID:   tid,
					Name: tname,
				})
			}
			if len(oncall) > 0 {
				covered = true
			}
			tiers = append(tiers, Tier{Level: t.Level, OnCall: oncall})
		}
		out = append(out, Policy{
			ID:      id,
			Name:    name,
			NumTier: len(tiers),
			Covered: covered,
			Tiers:   tiers,
		})
	}
	return out, nil
}

// normalizeKind maps a PagerDuty escalation-target type to the schema's
// assignee_kind enum. PagerDuty target types are e.g. "user_reference" /
// "schedule_reference"; anything not a schedule is treated as a user.
func normalizeKind(s string) string {
	if strings.Contains(strings.ToLower(s), "schedule") {
		return "schedule"
	}
	return "user"
}
