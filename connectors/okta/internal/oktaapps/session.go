package oktaapps

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// #2951: the global session policy (OKTA_SIGN_ON) governs how a user's
// Okta session is established, before any app sign-in policy is read. When
// it requires MFA for every user, every app's users have presented two
// factors, whatever the app's own policy asks.
//
// Okta evaluates policies in priority order (1 first). A policy whose
// conditions the user meets has its rules tried in priority order; the
// first rule whose conditions the user meets applies, and when none does,
// the next policy is tried (the Policy tag description in the management
// OpenAPI spec). The connector does not resolve group membership, so it
// treats every active rule as reachable except where it can show
// otherwise: a rule after a catch-all rule of the same policy, and every
// policy after one that includes the Everyone group and has an active
// catch-all rule.

type rawSessionPolicy struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Priority   *int   `json:"priority"`
	Conditions *struct {
		People *struct {
			Groups *struct {
				Include []string `json:"include"`
			} `json:"groups"`
		} `json:"people"`
	} `json:"conditions"`
}

func (p rawSessionPolicy) includes(groupID string) bool {
	if groupID == "" || p.Conditions == nil || p.Conditions.People == nil || p.Conditions.People.Groups == nil {
		return false
	}
	for _, g := range p.Conditions.People.Groups.Include {
		if g == groupID {
			return true
		}
	}
	return false
}

// sessionMFA decides whether the global session policy requires MFA for
// every user. It reads only the API's policy and rule lists, so it is run
// once per pull.
//
//   - false when a reachable active rule allows a session with
//     requireFactor false (Okta's default when the field is absent);
//   - true when every reachable active rule denies access or sets
//     requireFactor true, and at least one active policy exists;
//   - undecided otherwise, with a reason. A false still decides when
//     another policy or rule is undecided.
func sessionMFA(ctx context.Context, api API) mfaVerdict {
	policies, err := api.ListSessionPolicies(ctx)
	if err != nil {
		return mfaVerdict{reason: fmt.Sprintf("global session policies: %v", err)}
	}
	everyoneID, err := api.EveryoneGroupID(ctx)
	if err != nil {
		return mfaVerdict{reason: fmt.Sprintf("global session policy: everyone group: %v", err)}
	}
	if everyoneID == "" {
		return mfaVerdict{reason: "global session policy: everyone group not found among BUILT_IN groups"}
	}
	var active []rawSessionPolicy
	for _, p := range policies {
		if p.Status == "ACTIVE" {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return mfaVerdict{reason: "no active global session policy"}
	}
	ordered := byPriority(active, func(p rawSessionPolicy) *int { return p.Priority })

	no, yes := false, true
	undecided := ""
	for _, p := range active {
		rules, err := api.ListPolicyRules(ctx, p.ID)
		if err != nil {
			undecided = fmt.Sprintf("global session policy %s rules: %v", p.ID, err)
			continue
		}
		oneFA, why, catchAll := sessionRules(p.ID, rules)
		if oneFA {
			return mfaVerdict{required: &no}
		}
		if why != "" {
			undecided = why
		}
		if ordered && catchAll && p.includes(everyoneID) {
			break
		}
	}
	if undecided != "" {
		return mfaVerdict{reason: undecided}
	}
	return mfaVerdict{required: &yes}
}

// sessionRules reads one global session policy's rules in priority order
// and reports whether a reachable active rule allows a session without
// MFA, why a rule could not be read, and whether the policy has an active
// catch-all rule (so no user who meets its conditions falls through).
func sessionRules(policyID string, rules []rawRule) (oneFA bool, reason string, catchAll bool) {
	var active []rawRule
	for _, r := range rules {
		if r.Status == "ACTIVE" {
			active = append(active, r)
		}
	}
	ordered := byPriority(active, func(r rawRule) *int { return r.Priority })
	for _, r := range active {
		so := r.Actions.Signon
		switch {
		case so == nil:
			reason = fmt.Sprintf("global session policy %s rule %s has no signon action", policyID, r.ID)
		case so.Access == "DENY":
		case so.Access != "ALLOW":
			reason = fmt.Sprintf("global session policy %s rule %s has access %q", policyID, r.ID, so.Access)
		case so.RequireFactor == nil || !*so.RequireFactor:
			return true, "", false
		}
		if ordered && catchAllConditions(r.Conditions) {
			return false, reason, true
		}
	}
	return false, reason, false
}

// byPriority sorts xs by ascending priority in place and reports whether
// the order is known: every item has a priority and no two share one.
// When it is not, the caller treats nothing as unreachable.
func byPriority[T any](xs []T, prio func(T) *int) bool {
	seen := map[int]bool{}
	for _, x := range xs {
		p := prio(x)
		if p == nil || seen[*p] {
			return false
		}
		seen[*p] = true
	}
	sort.SliceStable(xs, func(i, j int) bool { return *prio(xs[i]) < *prio(xs[j]) })
	return true
}

// catchAllConditions reports whether a global session rule's conditions
// match every sign-in: absent, or every condition present in the form
// that selects everything. The forms are those of OktaSignOnPolicyRuleConditions
// (people, network, authContext, identityProvider) plus risk and riskScore,
// which the spec's list-all-sign-on-policy-rule-response example carries.
// Anything else, including a condition the connector does not know, is not
// catch-all, which leaves later rules reachable.
func catchAllConditions(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var conds map[string]map[string]any
	if err := json.Unmarshal(raw, &conds); err != nil {
		return false
	}
	for name, c := range conds {
		var ok bool
		switch name {
		case "people":
			ok = onlyKeys(c, map[string]func(any) bool{"users": emptyInclExcl, "groups": emptyInclExcl})
		case "network":
			ok = onlyKeys(c, map[string]func(any) bool{"connection": equals("ANYWHERE"), "include": emptyList, "exclude": emptyList})
		case "authContext":
			ok = onlyKeys(c, map[string]func(any) bool{"authType": equals("ANY")})
		case "identityProvider":
			ok = onlyKeys(c, map[string]func(any) bool{"provider": equals("ANY"), "idpIds": emptyList})
		case "risk":
			ok = onlyKeys(c, map[string]func(any) bool{"behaviors": emptyList})
		case "riskScore":
			ok = onlyKeys(c, map[string]func(any) bool{"level": equals("ANY")})
		}
		if !ok {
			return false
		}
	}
	return true
}

func onlyKeys(m map[string]any, allowed map[string]func(any) bool) bool {
	for k, v := range m {
		check, ok := allowed[k]
		if !ok || !check(v) {
			return false
		}
	}
	return true
}

func equals(want string) func(any) bool {
	return func(v any) bool { s, ok := v.(string); return ok && s == want }
}

func emptyList(v any) bool {
	if v == nil {
		return true
	}
	l, ok := v.([]any)
	return ok && len(l) == 0
}

func emptyInclExcl(v any) bool {
	if v == nil {
		return true
	}
	m, ok := v.(map[string]any)
	return ok && onlyKeys(m, map[string]func(any) bool{"include": emptyList, "exclude": emptyList})
}
