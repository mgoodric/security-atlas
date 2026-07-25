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

// fakeSkiptoken is an obviously-fake ARM continuation cursor — no real
// skiptoken / GUID / vendor secret (GitGuardian-neutral).
const fakeSkiptoken = "00000000-0000-0000-0000-00000000page2"

// TestClient_ListFirewallPolicies_FollowsPolicyListNextLink covers the
// firewallPolicies-list cursor walk: page1 carries a nextLink → page2 has none.
// Both policies (one per page) must be collected, and every request must be GET.
func TestClient_ListFirewallPolicies_FollowsPolicyListNextLink(t *testing.T) {
	var methods []string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch {
		case strings.Contains(r.URL.Path, "ruleCollectionGroups"):
			_, _ = w.Write([]byte(`{"value":[]}`))
		case r.URL.Query().Get("$skiptoken") == fakeSkiptoken:
			// page2 of the policy list — no nextLink (terminates the walk).
			_, _ = w.Write([]byte(`{
				"value": [
					{
						"id": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg2/providers/Microsoft.Network/firewallPolicies/fwpolicy2",
						"name": "fwpolicy2",
						"location": "westus"
					}
				]
			}`))
		default:
			// page1 of the policy list — carries an obviously-fake nextLink back to
			// this (non-Azure, 127.0.0.1) test server.
			next := srvURL + "/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Network/firewallPolicies?api-version=2024-01-01&$skiptoken=" + fakeSkiptoken
			_, _ = w.Write([]byte(`{
				"value": [
					{
						"id": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg1/providers/Microsoft.Network/firewallPolicies/fwpolicy1",
						"name": "fwpolicy1",
						"location": "eastus"
					}
				],
				"nextLink": "` + next + `"
			}`))
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("policies across pages = %d; want 2 (page1 + page2)", len(got))
	}
	if got[0].Name != "fwpolicy1" || got[1].Name != "fwpolicy2" {
		t.Errorf("policies not collected across pages in order: %+v", got)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("method = %s; want GET (nextLink follow-ups are GET too)", m)
		}
	}
}

// TestClient_ListFirewallPolicies_FollowsGroupNextLink covers the per-policy
// ruleCollectionGroups cursor walk: group page1 has a nextLink → page2 has none.
// Both groups must be collected onto the single policy.
func TestClient_ListFirewallPolicies_FollowsGroupNextLink(t *testing.T) {
	var methods []string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if !strings.Contains(r.URL.Path, "ruleCollectionGroups") {
			_, _ = w.Write([]byte(neutralPolicyPage))
			return
		}
		if r.URL.Query().Get("$skiptoken") == fakeSkiptoken {
			// group page2 — no nextLink.
			_, _ = w.Write([]byte(`{
				"value": [
					{ "name": "GroupOnPage2", "properties": { "priority": 400, "ruleCollections": [] } }
				]
			}`))
			return
		}
		// group page1 — carries an obviously-fake nextLink.
		next := srvURL + "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg1/providers/Microsoft.Network/firewallPolicies/fwpolicy1/ruleCollectionGroups?api-version=2024-01-01&$skiptoken=" + fakeSkiptoken
		_, _ = w.Write([]byte(`{
			"value": [
				{ "name": "GroupOnPage1", "properties": { "priority": 200, "ruleCollections": [] } }
			],
			"nextLink": "` + next + `"
		}`))
	}))
	defer srv.Close()
	srvURL = srv.URL

	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("policies = %d; want 1", len(got))
	}
	groups := got[0].RuleCollectionGroups
	if len(groups) != 2 {
		t.Fatalf("groups across pages = %d; want 2 (page1 + page2)", len(groups))
	}
	if groups[0].Name != "GroupOnPage1" || groups[1].Name != "GroupOnPage2" {
		t.Errorf("groups not collected across pages in order: %+v", groups)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("method = %s; want GET", m)
		}
	}
}

// TestClient_ListFirewallPolicies_GroupNextLinkLoopTerminates pins the
// loop-termination DoS backstop: a ruleCollectionGroups nextLink that points to
// itself (a hostile / buggy self-referential cursor) terminates after the
// per-policy page cap rather than running forever. The connector reports what it
// gathered (cap-hit is not an error).
func TestClient_ListFirewallPolicies_GroupNextLinkLoopTerminates(t *testing.T) {
	var groupReads int
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "ruleCollectionGroups") {
			_, _ = w.Write([]byte(neutralPolicyPage))
			return
		}
		groupReads++
		// Every group page points its nextLink straight back at itself — a cursor
		// that never terminates on its own. The per-policy page cap must break it.
		self := srvURL + "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg1/providers/Microsoft.Network/firewallPolicies/fwpolicy1/ruleCollectionGroups?api-version=2024-01-01&$skiptoken=" + fakeSkiptoken
		_, _ = w.Write([]byte(`{
			"value": [
				{ "name": "Loop", "properties": { "priority": 100, "ruleCollections": [] } }
			],
			"nextLink": "` + self + `"
		}`))
	}))
	defer srv.Close()
	srvURL = srv.URL

	c := firewall.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListFirewallPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListFirewallPolicies: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("policies = %d; want 1", len(got))
	}
	// The self-pointing nextLink terminated at the page cap, NOT forever.
	if groupReads > firewall.MaxRuleCollectionGroupPagesForTest() {
		t.Fatalf("group reads = %d; expected to stop at the per-policy page cap %d (loop must terminate)",
			groupReads, firewall.MaxRuleCollectionGroupPagesForTest())
	}
	// One group was gathered per page until the cap; the connector reported what
	// it collected without erroring.
	if got[0].ReadError != "" {
		t.Errorf("cap-hit must not be a read error; got %q", got[0].ReadError)
	}
	if len(got[0].RuleCollectionGroups) == 0 {
		t.Error("expected the gathered-before-cap groups to be reported")
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
