package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ARMScope is the OAuth2 resource scope for read-only Azure Resource Manager —
// the SAME scope the storage, AKS, NSG and Key-Vault kinds use (no new Azure
// scope, P0-614-1).
const ARMScope = "https://management.azure.com/.default"

// armAPIVersion pins the Network Resource Provider API version the connector
// reads firewallPolicies + ruleCollectionGroups against.
const armAPIVersion = "2024-01-01"

// maxRuleCollectionGroupsPerPolicy bounds the per-policy ruleCollectionGroups
// collected across ALL cursor pages (DoS guard, threat-model D). Slice 634 added
// nextLink pagination, so the connector now follows the ARM nextLink cursor up to
// this many rule-collection groups for one policy; a policy with more than this
// many groups has its list truncated honestly at the cap.
const maxRuleCollectionGroupsPerPolicy = 200

// maxRuleCollectionGroupsPerRun caps the TOTAL rule-collection groups enumerated
// across all firewall policies in one connector run (DoS guard, threat-model D).
// Once the run reaches the cap, further per-policy rule-collection-group reads
// are skipped (the policy still reports; its rule-collection-group list is
// simply truncated). This is the run-wide DoS backstop the per-run cap names; it
// is UNCHANGED by slice 634's nextLink pagination (P0-634-2).
const maxRuleCollectionGroupsPerRun = 2000

// maxFirewallPolicyPages caps the firewallPolicies-list nextLink walk
// (slice 634). It is the loop-termination / DoS backstop for a pathological or
// self-pointing nextLink on the policy list: a malicious or buggy nextLink that
// points to itself terminates after this many pages rather than looping forever
// (P0-634-2). Hitting the cap is not an error — the connector reports the
// policies it gathered.
const maxFirewallPolicyPages = 100

// maxRuleCollectionGroupPages caps the per-policy ruleCollectionGroups nextLink
// walk (slice 634). Independent of the record caps above, it is the
// loop-termination backstop so a self-pointing or non-terminating
// ruleCollectionGroups nextLink chain cannot drive an unbounded read loop for a
// single policy — the walk stops after this many pages even if the record caps
// are never reached (e.g. empty pages with a recurring nextLink). Hitting the
// cap is not an error.
const maxRuleCollectionGroupPages = 100

// Client is a thin read-only HTTP client for the ARM firewallPolicies list +
// ruleCollectionGroups list endpoints. It holds a short-lived bearer token
// (never logged) and issues only GET requests against the rule-config surface.
// It NEVER POSTs and NEVER mutates a network resource (P0-614-3); it NEVER reads
// flow logs, packet captures, traffic contents, NAT-rule secrets, threat-intel
// feeds, or route tables (P0-614-2). v0 reads the first bounded page of firewall
// policies for one subscription.
type Client struct {
	HTTP           *http.Client
	BaseURL        string // default https://management.azure.com
	SubscriptionID string
	token          string
}

// NewClient builds an ARM client. token is a bearer access token (from
// azureauth.Credential.AcquireToken). baseURL empty defaults to the public ARM
// endpoint.
func NewClient(httpClient *http.Client, baseURL, subscriptionID, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://management.azure.com"
	}
	return &Client{
		HTTP:           httpClient,
		BaseURL:        strings.TrimRight(baseURL, "/"),
		SubscriptionID: subscriptionID,
		token:          token,
	}
}

// armFirewallPolicy mirrors the ARM FirewallPolicy resource (the list surface
// returns the policy identity only — its rule-collection groups are a separate
// scoped read). Management-plane CONFIGURATION only.
type armFirewallPolicy struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

type armFirewallPolicyPage struct {
	Value []armFirewallPolicy `json:"value"`
	// NextLink is the ARM continuation cursor: an absolute URL carrying an opaque
	// skiptoken that must be requested verbatim (not reconstructed). Empty on the
	// last page.
	NextLink string `json:"nextLink"`
}

// armRuleCollectionGroup mirrors one Microsoft.Network/firewallPolicies/
// ruleCollectionGroups entry. CONFIGURATION metadata only — the group priority,
// its rule collections, and each collection's rules (protocols, source/dest
// prefixes, ports, target FQDNs). NEVER flow logs, packet captures, traffic
// contents, NAT-rule secrets, threat-intel feeds, or route tables.
type armRuleCollectionGroup struct {
	Name       string `json:"name"`
	Properties struct {
		Priority        int                 `json:"priority"`
		RuleCollections []armRuleCollection `json:"ruleCollections"`
	} `json:"properties"`
}

// armRuleCollection mirrors one ruleCollection inside a group. The
// ruleCollectionType discriminates network vs application; the action lives in
// the nested "action" object.
type armRuleCollection struct {
	Name               string `json:"name"`
	RuleCollectionType string `json:"ruleCollectionType"`
	Priority           int    `json:"priority"`
	Action             struct {
		Type string `json:"type"`
	} `json:"action"`
	Rules []armRule `json:"rules"`
}

// armRule mirrors one rule inside a collection. ruleType discriminates
// NetworkRule vs ApplicationRule; both carry rule CONFIGURATION metadata only.
type armRule struct {
	Name                 string        `json:"name"`
	RuleType             string        `json:"ruleType"`
	IPProtocols          []string      `json:"ipProtocols"`
	Protocols            []armAppProto `json:"protocols"`
	SourceAddresses      []string      `json:"sourceAddresses"`
	DestinationAddresses []string      `json:"destinationAddresses"`
	DestinationPorts     []string      `json:"destinationPorts"`
	TargetFqdns          []string      `json:"targetFqdns"`
}

// armAppProto is one application-rule protocol entry (protocolType + port).
type armAppProto struct {
	ProtocolType string `json:"protocolType"`
}

type armRuleCollectionGroupPage struct {
	Value []armRuleCollectionGroup `json:"value"`
	// NextLink is the ARM continuation cursor for the ruleCollectionGroups list
	// (see armFirewallPolicyPage.NextLink). Empty on the last page.
	NextLink string `json:"nextLink"`
}

// ListFirewallPolicies enumerates the Azure Firewall policies in the
// subscription by following the ARM nextLink cursor across the firewallPolicies
// list pages (slice 634), then reads each policy's rule-collection groups via a
// scoped read-only ARM read (itself nextLink-paginated). Read-only
// (firewallPolicies + ruleCollectionGroups list, ARM Reader role). Every request
// — first page and every nextLink follow-up — is a GET against the list surfaces
// only; it never mutates and never reads flow logs / NAT secrets / threat-intel
// feeds (P0-634-1, P0-634-3).
//
// The policy-list walk is bounded by maxFirewallPolicyPages so a pathological /
// self-pointing nextLink terminates (P0-634-2); the per-policy group reads stay
// bounded by the run-wide maxRuleCollectionGroupsPerRun DoS backstop, which slice
// 634 does NOT remove.
func (c *Client) ListFirewallPolicies(ctx context.Context) ([]RawPolicy, error) {
	next := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.Network/firewallPolicies?api-version=%s",
		c.BaseURL, url.PathEscape(c.SubscriptionID), armAPIVersion)

	var out []RawPolicy
	var groupsThisRun int
	for page := 0; page < maxFirewallPolicyPages; page++ {
		fpPage, err := c.getFirewallPolicyPage(ctx, next)
		if err != nil {
			return nil, err
		}
		for _, p := range fpPage.Value {
			rp := RawPolicy{
				ID:            p.ID,
				Name:          p.Name,
				ResourceGroup: resourceGroupFromID(p.ID),
				Location:      p.Location,
			}
			if p.ID != "" {
				groups, n, rerr := c.listRuleCollectionGroups(ctx, p.ID, &groupsThisRun)
				if rerr != "" {
					// A per-policy ruleCollectionGroups read error marks the policy
					// INCONCLUSIVE (the verdict() path) rather than dropping it — the
					// same fail-soft contract the Key-Vault RBAC read uses.
					rp.ReadError = rerr
				}
				groupsThisRun += n
				rp.RuleCollectionGroups = groups
			}
			out = append(out, rp)
		}
		if strings.TrimSpace(fpPage.NextLink) == "" {
			break
		}
		// The server-issued nextLink is an absolute URL carrying an opaque
		// skiptoken — follow it verbatim.
		next = fpPage.NextLink
	}
	return out, nil
}

func (c *Client) getFirewallPolicyPage(ctx context.Context, u string) (*armFirewallPolicyPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var page armFirewallPolicyPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode firewall policies: %w", err)
	}
	return &page, nil
}

// listRuleCollectionGroups reads the ruleCollectionGroups scoped to one firewall
// policy resource id, following the ARM nextLink cursor across pages (slice 634),
// and maps each into a RuleCollectionGroup (priority + its network/application
// rule collections). CONFIGURATION metadata only — never a flow log, packet
// capture, traffic content, NAT-rule secret, threat-intel feed, or route table
// (P0-614-2 / P0-634-1); ARM Reader suffices.
//
// Bounded by construction (DoS guard, threat-model D), UNCHANGED in WHAT it
// collects by slice 634 — only HOW MANY pages:
//   - the per-policy record cap (maxRuleCollectionGroupsPerPolicy) truncates the
//     collected groups for one policy across all its pages;
//   - the run-wide cap (maxRuleCollectionGroupsPerRun) stops the walk entirely
//     once the run total is reached (the DoS backstop slice 634 keeps);
//   - the per-policy page cap (maxRuleCollectionGroupPages) is the loop-
//     termination backstop so a self-pointing / non-terminating nextLink
//     terminates (P0-634-2).
//
// Every request — first page and every nextLink follow-up — is a GET. On read
// error it returns the error STRING (so the caller can mark the policy
// INCONCLUSIVE) rather than failing the whole run — one throttled policy must
// not blind the connector to the rest of the estate. Groups gathered before an
// error on a later page are discarded so the policy is verdicted INCONCLUSIVE
// (partial-read honesty) rather than reported as a complete-but-truncated set.
func (c *Client) listRuleCollectionGroups(ctx context.Context, policyID string, runTotal *int) ([]RuleCollectionGroup, int, string) {
	if *runTotal >= maxRuleCollectionGroupsPerRun {
		return nil, 0, ""
	}
	next := fmt.Sprintf("%s%s/ruleCollectionGroups?api-version=%s", c.BaseURL, policyID, armAPIVersion)

	groups := make([]RuleCollectionGroup, 0)
	for page := 0; page < maxRuleCollectionGroupPages; page++ {
		rcgPage, rerr := c.getRuleCollectionGroupPage(ctx, next)
		if rerr != "" {
			return nil, 0, rerr
		}
		for _, g := range rcgPage.Value {
			if len(groups) >= maxRuleCollectionGroupsPerPolicy {
				break
			}
			if *runTotal+len(groups) >= maxRuleCollectionGroupsPerRun {
				break
			}
			groups = append(groups, RuleCollectionGroup{
				Name:            g.Name,
				Priority:        g.Properties.Priority,
				RuleCollections: mapRuleCollections(g.Properties.RuleCollections),
			})
		}
		// Stop following the cursor once a cap is reached or the cursor is spent.
		if len(groups) >= maxRuleCollectionGroupsPerPolicy ||
			*runTotal+len(groups) >= maxRuleCollectionGroupsPerRun ||
			strings.TrimSpace(rcgPage.NextLink) == "" {
			break
		}
		// The server-issued nextLink is an absolute URL carrying an opaque
		// skiptoken — follow it verbatim.
		next = rcgPage.NextLink
	}
	return groups, len(groups), ""
}

// getRuleCollectionGroupPage GETs one ruleCollectionGroups page (first page or a
// nextLink follow-up). It returns the error as a STRING so the caller can mark
// the policy INCONCLUSIVE rather than failing the whole run.
func (c *Client) getRuleCollectionGroupPage(ctx context.Context, u string) (*armRuleCollectionGroupPage, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err.Error()
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, (&APIError{Status: res.StatusCode, Body: drain(res.Body)}).Error()
	}
	var page armRuleCollectionGroupPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode rule collection groups: %w", err).Error()
	}
	return &page, ""
}

// mapRuleCollections normalizes the ARM ruleCollection list into the connector's
// CONFIGURATION-only shape.
func mapRuleCollections(in []armRuleCollection) []RuleCollection {
	if len(in) == 0 {
		return nil
	}
	out := make([]RuleCollection, 0, len(in))
	for _, rc := range in {
		out = append(out, RuleCollection{
			Name:     rc.Name,
			Type:     collectionType(rc.Rules),
			Action:   strings.ToLower(rc.Action.Type),
			Priority: rc.Priority,
			Rules:    mapRules(rc.Rules),
		})
	}
	return out
}

// collectionType derives the connector's "network" | "application" shape from
// the rules the collection holds. Azure tags both network and application filter
// collections with the same ruleCollectionType ("FilterRuleCollection"); the
// network-vs-application distinction is carried per-rule via ruleType
// (NetworkRule vs ApplicationRule), so the collection type is inferred from its
// first rule.
func collectionType(rules []armRule) string {
	for _, r := range rules {
		low := strings.ToLower(r.RuleType)
		switch {
		case strings.Contains(low, "application"):
			return "application"
		case strings.Contains(low, "network"):
			return "network"
		}
	}
	return ""
}

// mapRules normalizes the ARM rule list into the connector's rule-METADATA-only
// shape. Both NetworkRule and ApplicationRule shapes are flattened; the network
// protocols come from ipProtocols, the application protocols from protocols[].
func mapRules(in []armRule) []Rule {
	if len(in) == 0 {
		return nil
	}
	out := make([]Rule, 0, len(in))
	for _, r := range in {
		out = append(out, Rule{
			Name:                 r.Name,
			Protocols:            ruleProtocols(r),
			SourceAddresses:      r.SourceAddresses,
			DestinationAddresses: r.DestinationAddresses,
			DestinationPorts:     r.DestinationPorts,
			DestinationFQDNs:     r.TargetFqdns,
		})
	}
	return out
}

// ruleProtocols flattens the network-rule ipProtocols and the application-rule
// protocols[] into a single lower-cased protocol-name list.
func ruleProtocols(r armRule) []string {
	var out []string
	for _, p := range r.IPProtocols {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	for _, p := range r.Protocols {
		if p.ProtocolType = strings.TrimSpace(p.ProtocolType); p.ProtocolType != "" {
			out = append(out, strings.ToLower(p.ProtocolType))
		}
	}
	return out
}

// resourceGroupFromID extracts the resource-group segment from an ARM resource
// id (.../resourceGroups/<rg>/providers/...). Returns "" when absent.
func resourceGroupFromID(id string) string {
	parts := strings.Split(id, "/")
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "resourceGroups") {
			return parts[i+1]
		}
	}
	return ""
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
}

// APIError carries ARM REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("arm: HTTP %d", e.Status)
	}
	return fmt.Sprintf("arm: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
