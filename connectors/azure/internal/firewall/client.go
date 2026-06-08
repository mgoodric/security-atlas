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
// read (DoS guard, threat-model D). ARM returns a single bounded page; the
// connector truncates defensively. A huge estate that legitimately exceeds this
// needs the shared cursor-pagination follow-on — pagination is deliberately NOT
// implemented here.
const maxRuleCollectionGroupsPerPolicy = 200

// maxRuleCollectionGroupsPerRun caps the TOTAL rule-collection groups enumerated
// across all firewall policies in one connector run (DoS guard, threat-model D).
// Once the run reaches the cap, further per-policy rule-collection-group reads
// are skipped (the policy still reports; its rule-collection-group list is
// simply truncated).
const maxRuleCollectionGroupsPerRun = 2000

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
}

// ListFirewallPolicies fetches the first page of Azure Firewall policies in the
// subscription, then reads each policy's rule-collection groups via a scoped
// read-only ARM read. Read-only (firewallPolicies + ruleCollectionGroups list,
// ARM Reader role). This is a GET against the list surfaces only — it never
// mutates and never reads flow logs / NAT secrets / threat-intel feeds.
func (c *Client) ListFirewallPolicies(ctx context.Context) ([]RawPolicy, error) {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.Network/firewallPolicies?api-version=%s",
		c.BaseURL, url.PathEscape(c.SubscriptionID), armAPIVersion)
	page, err := c.getFirewallPolicyPage(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make([]RawPolicy, 0, len(page.Value))
	var groupsThisRun int
	for _, p := range page.Value {
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
// policy resource id and maps each into a RuleCollectionGroup (priority + its
// network/application rule collections). CONFIGURATION metadata only — never a
// flow log, packet capture, traffic content, NAT-rule secret, threat-intel feed,
// or route table (P0-614-2); ARM Reader suffices (P0-614-1).
//
// Bounded by construction (DoS guard, threat-model D): a single bounded page,
// truncated to maxRuleCollectionGroupsPerPolicy, and skipped entirely once the
// run-wide cap (maxRuleCollectionGroupsPerRun) is reached. Cursor pagination for
// a huge estate is a documented follow-on, NOT implemented here.
//
// On read error it returns the error STRING (so the caller can mark the policy
// INCONCLUSIVE) rather than failing the whole run — one throttled policy must
// not blind the connector to the rest of the estate.
func (c *Client) listRuleCollectionGroups(ctx context.Context, policyID string, runTotal *int) ([]RuleCollectionGroup, int, string) {
	if *runTotal >= maxRuleCollectionGroupsPerRun {
		return nil, 0, ""
	}
	u := fmt.Sprintf("%s%s/ruleCollectionGroups?api-version=%s", c.BaseURL, policyID, armAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err.Error()
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err.Error()
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, 0, (&APIError{Status: res.StatusCode, Body: drain(res.Body)}).Error()
	}
	var page armRuleCollectionGroupPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, 0, fmt.Errorf("decode rule collection groups: %w", err).Error()
	}
	groups := make([]RuleCollectionGroup, 0, len(page.Value))
	for _, g := range page.Value {
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
	return groups, len(groups), ""
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
