package firewall_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/firewall"
)

// neutralPolicyPage is a NEUTRAL fixture — obviously-fake 00000000-... ids, no
// real subscription GUIDs / secrets / vendor tokens.
const neutralPolicyPage = `{
	"value": [
		{
			"id": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg1/providers/Microsoft.Network/firewallPolicies/fwpolicy1",
			"name": "fwpolicy1",
			"location": "eastus"
		}
	]
}`

// neutralGroupPage is the ruleCollectionGroups list for fwpolicy1 — a network
// rule collection plus an application rule collection. CONFIGURATION only.
const neutralGroupPage = `{
	"value": [
		{
			"name": "DefaultNetworkRuleCollectionGroup",
			"properties": {
				"priority": 200,
				"ruleCollections": [
					{
						"name": "allow-corp-ssh",
						"ruleCollectionType": "FirewallPolicyFilterRuleCollection",
						"priority": 100,
						"action": { "type": "Allow" },
						"rules": [
							{
								"name": "ssh-corp",
								"ruleType": "NetworkRule",
								"ipProtocols": ["TCP"],
								"sourceAddresses": ["203.0.113.0/24"],
								"destinationAddresses": ["10.0.0.0/8"],
								"destinationPorts": ["22"]
							}
						]
					}
				]
			}
		},
		{
			"name": "DefaultApplicationRuleCollectionGroup",
			"properties": {
				"priority": 300,
				"ruleCollections": [
					{
						"name": "allow-fqdn",
						"ruleCollectionType": "FirewallPolicyFilterRuleCollection",
						"priority": 100,
						"action": { "type": "Allow" },
						"rules": [
							{
								"name": "to-updates",
								"ruleType": "ApplicationRule",
								"protocols": [{ "protocolType": "Https", "port": 443 }],
								"sourceAddresses": ["10.0.0.0/8"],
								"targetFqdns": ["updates.example.com"]
							}
						]
					}
				]
			}
		}
	]
}`

func TestClient_ListFirewallPolicies_ParsesARMSurface(t *testing.T) {
	var methods []string
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		if strings.Contains(r.URL.Path, "ruleCollectionGroups") {
			_, _ = w.Write([]byte(neutralGroupPage))
			return
		}
		_, _ = w.Write([]byte(neutralPolicyPage))
	}))
	defer srv.Close()

	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "test-access-token")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	p := got[0]
	if p.Name != "fwpolicy1" || p.ResourceGroup != "rg1" || p.Location != "eastus" {
		t.Errorf("policy fields wrong: %+v", p)
	}
	if len(p.RuleCollectionGroups) != 2 {
		t.Fatalf("rule-collection groups = %d; want 2", len(p.RuleCollectionGroups))
	}
	// Group priority ordering preserved.
	if p.RuleCollectionGroups[0].Priority != 200 || p.RuleCollectionGroups[1].Priority != 300 {
		t.Errorf("group priority ordering wrong: %+v", p.RuleCollectionGroups)
	}
	netCol := p.RuleCollectionGroups[0].RuleCollections[0]
	if netCol.Name != "allow-corp-ssh" || netCol.Type != "network" || netCol.Action != "allow" || netCol.Priority != 100 {
		t.Errorf("network collection wrong: %+v", netCol)
	}
	netRule := netCol.Rules[0]
	if netRule.Name != "ssh-corp" || len(netRule.Protocols) != 1 || netRule.Protocols[0] != "tcp" ||
		netRule.DestinationPorts[0] != "22" {
		t.Errorf("network rule wrong: %+v", netRule)
	}
	appCol := p.RuleCollectionGroups[1].RuleCollections[0]
	if appCol.Type != "application" {
		t.Errorf("application collection type wrong: %+v", appCol)
	}
	appRule := appCol.Rules[0]
	if appRule.Name != "to-updates" || len(appRule.Protocols) != 1 || appRule.Protocols[0] != "https" ||
		appRule.DestinationFQDNs[0] != "updates.example.com" {
		t.Errorf("application rule wrong: %+v", appRule)
	}

	// P0-614-3: the connector must issue GET only — never mutate a network
	// resource. Both the policy list and the per-policy group read are GETs.
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only, P0-614-3)", m)
		}
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 reads (policy list + 1 group read); got %v", paths)
	}
	if !strings.Contains(paths[0], "/subscriptions/test-sub/") || !strings.Contains(paths[0], "firewallPolicies") {
		t.Errorf("policy-list path wrong: %q", paths[0])
	}
	if !strings.Contains(paths[1], "ruleCollectionGroups") {
		t.Errorf("group-read path wrong: %q", paths[1])
	}
}

func TestClient_ListFirewallPolicies_GroupReadErrorMarksInconclusive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "ruleCollectionGroups") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(neutralPolicyPage))
	}))
	defer srv.Close()
	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	// The policy is still returned (fail-soft) but flagged with a ReadError so
	// the collector verdicts it INCONCLUSIVE.
	if len(got) != 1 || got[0].ReadError == "" {
		t.Fatalf("expected policy with ReadError; got %+v", got)
	}
}

func TestClient_ListFirewallPolicies_EmptyPolicies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer srv.Close()
	c := firewall.NewClient(nil, srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no policies; got %+v", got)
	}
}

func TestClient_ListFirewallPolicies_PolicyListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListFirewallPolicies(context.Background()); err == nil {
		t.Fatal("expected HTTP 403 error")
	}
}

func TestClient_ListFirewallPolicies_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListFirewallPolicies(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestClient_ListFirewallPolicies_AuthHeader(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sawAuth == "" {
			sawAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer srv.Close()
	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "test-access-token")
	if _, err := c.ListFirewallPolicies(context.Background()); err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if sawAuth != "Bearer test-access-token" {
		t.Errorf("auth header = %q", sawAuth)
	}
}

func TestClient_ListFirewallPolicies_GroupBadJSONMarksInconclusive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "ruleCollectionGroups") {
			_, _ = w.Write([]byte(`not json`))
			return
		}
		_, _ = w.Write([]byte(neutralPolicyPage))
	}))
	defer srv.Close()
	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 1 || got[0].ReadError == "" {
		t.Fatalf("expected policy with decode ReadError; got %+v", got)
	}
}

// TestClient_ListFirewallPolicies_EmptyAndUntypedCollections covers the
// empty-rule-collection branch and the collectionType="" branch (a collection
// whose rules carry no recognizable ruleType).
func TestClient_ListFirewallPolicies_EmptyAndUntypedCollections(t *testing.T) {
	const groups = `{
		"value": [
			{
				"name": "EmptyGroup",
				"properties": { "priority": 100, "ruleCollections": [] }
			},
			{
				"name": "UntypedGroup",
				"properties": {
					"priority": 200,
					"ruleCollections": [
						{
							"name": "untyped",
							"ruleCollectionType": "FirewallPolicyFilterRuleCollection",
							"priority": 100,
							"action": { "type": "Deny" },
							"rules": [{ "name": "r", "ruleType": "Mystery" }]
						}
					]
				}
			}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "ruleCollectionGroups") {
			_, _ = w.Write([]byte(groups))
			return
		}
		_, _ = w.Write([]byte(neutralPolicyPage))
	}))
	defer srv.Close()
	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 1 || len(got[0].RuleCollectionGroups) != 2 {
		t.Fatalf("expected 2 groups; got %+v", got)
	}
	// EmptyGroup yields no collections; UntypedGroup yields one collection with an
	// empty Type (the ruleType was unrecognized).
	if len(got[0].RuleCollectionGroups[0].RuleCollections) != 0 {
		t.Errorf("empty group should carry no collections: %+v", got[0].RuleCollectionGroups[0])
	}
	untyped := got[0].RuleCollectionGroups[1].RuleCollections[0]
	if untyped.Type != "" || untyped.Action != "deny" {
		t.Errorf("untyped collection wrong: %+v", untyped)
	}
}

func TestNewClient_DefaultsBaseURL(t *testing.T) {
	c := firewall.NewClient(nil, "", "test-sub", "tok")
	if c.BaseURL != "https://management.azure.com" {
		t.Errorf("default base URL = %q", c.BaseURL)
	}
}

func TestAPIError_Message(t *testing.T) {
	if (&firewall.APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if (&firewall.APIError{Status: 403, Body: "denied"}).Error() == "" {
		t.Error("empty error message with body")
	}
}
