package firewall_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/firewall"
)

// fakeARM is a faked Azure Resource Manager surface — NO live Azure in tests.
type fakeARM struct {
	policies []firewall.RawPolicy
	err      error
}

func (f *fakeARM) ListFirewallPolicies(_ context.Context) ([]firewall.RawPolicy, error) {
	return f.policies, f.err
}

var fixedNow = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

// segmented is a well-segmented firewall policy: an allow network rule scoped to
// a corp prefix plus an application allow rule to a known FQDN, ordered behind a
// deny-all collection.
func segmented(id, name string) firewall.RawPolicy {
	return firewall.RawPolicy{
		ID: id, Name: name, ResourceGroup: "rg", Location: "eastus",
		RuleCollectionGroups: []firewall.RuleCollectionGroup{
			{
				Name: "DefaultNetworkRuleCollectionGroup", Priority: 200,
				RuleCollections: []firewall.RuleCollection{
					{
						Name: "allow-corp-ssh", Type: "network", Action: "allow", Priority: 100,
						Rules: []firewall.Rule{
							{Name: "ssh-corp", Protocols: []string{"tcp"},
								SourceAddresses: []string{"203.0.113.0/24"}, DestinationPorts: []string{"22"}},
						},
					},
					{
						Name: "deny-all", Type: "network", Action: "deny", Priority: 4096,
						Rules: []firewall.Rule{
							{Name: "deny", Protocols: []string{"any"},
								SourceAddresses: []string{"*"}, DestinationPorts: []string{"*"}},
						},
					},
				},
			},
			{
				Name: "DefaultApplicationRuleCollectionGroup", Priority: 300,
				RuleCollections: []firewall.RuleCollection{
					{
						Name: "allow-fqdn", Type: "application", Action: "allow", Priority: 100,
						Rules: []firewall.Rule{
							{Name: "to-updates", Protocols: []string{"https"},
								SourceAddresses: []string{"10.0.0.0/8"}, DestinationFQDNs: []string{"updates.example.com"}},
						},
					},
				},
			},
		},
	}
}

func TestInspect_PassWhenSegmented(t *testing.T) {
	api := &fakeARM{policies: []firewall.RawPolicy{segmented("/sub/fw1", "fw1")}}
	got, err := firewall.Inspect(context.Background(), api, "sub-1", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 || got[0].Result != firewall.ResultPass {
		t.Fatalf("want 1 PASS; got %+v", got)
	}
	if got[0].SubscriptionID != "sub-1" {
		t.Errorf("subscription = %q; want sub-1", got[0].SubscriptionID)
	}
	if len(got[0].RuleCollectionGroups) != 2 {
		t.Errorf("rule-collection groups not preserved: %+v", got[0])
	}
	// Priority ordering preserved (rule-collection-group ordering is the slice
	// signal): the two groups keep their 200 / 300 priorities.
	if got[0].RuleCollectionGroups[0].Priority != 200 || got[0].RuleCollectionGroups[1].Priority != 300 {
		t.Errorf("group priority ordering not preserved: %+v", got[0].RuleCollectionGroups)
	}
}

func TestInspect_FailOnOpenManagementPort(t *testing.T) {
	cases := []struct {
		name string
		rule firewall.Rule
	}{
		{"ssh-any-star", firewall.Rule{Name: "bad-ssh", SourceAddresses: []string{"*"}, DestinationPorts: []string{"22"}}},
		{"rdp-internet", firewall.Rule{Name: "bad-rdp", SourceAddresses: []string{"Internet"}, DestinationPorts: []string{"3389"}}},
		{"ssh-quad-zero", firewall.Rule{Name: "bad-ssh2", SourceAddresses: []string{"0.0.0.0/0"}, DestinationPorts: []string{"22"}}},
		{"any-port-range", firewall.Rule{Name: "wide", SourceAddresses: []string{"*"}, DestinationPorts: []string{"0-65535"}}},
		{"explicit-range", firewall.Rule{Name: "range", SourceAddresses: []string{"any"}, DestinationPorts: []string{"20-25"}}},
		{"star-port", firewall.Rule{Name: "all-ports", SourceAddresses: []string{"*"}, DestinationPorts: []string{"*"}}},
		{"multi-source-one-any", firewall.Rule{Name: "mixed", SourceAddresses: []string{"10.0.0.0/8", "*"}, DestinationPorts: []string{"3389"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := firewall.RawPolicy{ID: "/sub/fw", Name: "fw", RuleCollectionGroups: []firewall.RuleCollectionGroup{
				{Name: "g", Priority: 100, RuleCollections: []firewall.RuleCollection{
					{Name: "c", Type: "network", Action: "Allow", Priority: 100, Rules: []firewall.Rule{tc.rule}},
				}},
			}}
			got, err := firewall.Inspect(context.Background(), &fakeARM{policies: []firewall.RawPolicy{p}}, "sub", fixedNow)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if got[0].Result != firewall.ResultFail {
				t.Errorf("result = %q; want fail (open management port)", got[0].Result)
			}
			if !strings.Contains(got[0].Reason, "management port") {
				t.Errorf("reason = %q; want management-port reason", got[0].Reason)
			}
		})
	}
}

func TestInspect_PassWhenManagementPortRestricted(t *testing.T) {
	cases := []struct {
		name       string
		collection firewall.RuleCollection
	}{
		{"corp-source", firewall.RuleCollection{Name: "ok", Type: "network", Action: "allow", Rules: []firewall.Rule{
			{Name: "ssh", SourceAddresses: []string{"10.0.0.0/8"}, DestinationPorts: []string{"22"}}}}},
		{"deny-collection-from-any", firewall.RuleCollection{Name: "deny", Type: "network", Action: "deny", Rules: []firewall.Rule{
			{Name: "ssh", SourceAddresses: []string{"*"}, DestinationPorts: []string{"22"}}}}},
		{"non-mgmt-port", firewall.RuleCollection{Name: "web", Type: "network", Action: "allow", Rules: []firewall.Rule{
			{Name: "https", SourceAddresses: []string{"*"}, DestinationPorts: []string{"443"}}}}},
		{"non-mgmt-range", firewall.RuleCollection{Name: "web", Type: "network", Action: "allow", Rules: []firewall.Rule{
			{Name: "web", SourceAddresses: []string{"*"}, DestinationPorts: []string{"80-443"}}}}},
		{"application-rule-no-ports", firewall.RuleCollection{Name: "app", Type: "application", Action: "allow", Rules: []firewall.Rule{
			{Name: "fqdn", SourceAddresses: []string{"*"}, DestinationFQDNs: []string{"example.com"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := firewall.RawPolicy{ID: "/sub/fw", Name: "fw", RuleCollectionGroups: []firewall.RuleCollectionGroup{
				{Name: "g", Priority: 100, RuleCollections: []firewall.RuleCollection{tc.collection}},
			}}
			got, _ := firewall.Inspect(context.Background(), &fakeARM{policies: []firewall.RawPolicy{p}}, "sub", fixedNow)
			if got[0].Result != firewall.ResultPass {
				t.Errorf("result = %q; want pass (management port not Internet-exposed)", got[0].Result)
			}
		})
	}
}

func TestInspect_InconclusiveOnReadError(t *testing.T) {
	p := segmented("/sub/fw", "fw")
	p.ReadError = "throttled"
	got, err := firewall.Inspect(context.Background(), &fakeARM{policies: []firewall.RawPolicy{p}}, "sub", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != firewall.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", got[0].Result)
	}
}

func TestInspect_SkipsIncompletePolicies(t *testing.T) {
	api := &fakeARM{policies: []firewall.RawPolicy{
		{ID: "", Name: "x"},
		{ID: "y", Name: ""},
		segmented("/sub/ok", "ok"),
	}}
	got, _ := firewall.Inspect(context.Background(), api, "sub", fixedNow)
	if len(got) != 1 || got[0].PolicyName != "ok" {
		t.Fatalf("expected 1 valid policy; got %+v", got)
	}
}

func TestInspect_PropagatesListError(t *testing.T) {
	sentinel := errors.New("arm 403")
	_, err := firewall.Inspect(context.Background(), &fakeARM{err: sentinel}, "sub", fixedNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestInspect_NilAPIRejected(t *testing.T) {
	_, err := firewall.Inspect(context.Background(), nil, "sub", nil)
	if err == nil {
		t.Fatal("expected error for nil API")
	}
}

// TestInspect_PortEdgeCases pins the parseRange / atoi edge branches: a
// non-numeric port token, a reversed range, and an empty port string never
// match a management port (so a rule with only those ports PASSes).
func TestInspect_PortEdgeCases(t *testing.T) {
	for _, port := range []string{"abc", "99-22", "", "x-y"} {
		p := firewall.RawPolicy{ID: "/sub/fw", Name: "fw", RuleCollectionGroups: []firewall.RuleCollectionGroup{
			{Name: "g", Priority: 100, RuleCollections: []firewall.RuleCollection{
				{Name: "c", Type: "network", Action: "allow", Rules: []firewall.Rule{
					{Name: "r", SourceAddresses: []string{"*"}, DestinationPorts: []string{port}}}}}},
		}}
		got, _ := firewall.Inspect(context.Background(), &fakeARM{policies: []firewall.RawPolicy{p}}, "sub", fixedNow)
		if got[0].Result != firewall.ResultPass {
			t.Errorf("port %q should not match a management port; got %q", port, got[0].Result)
		}
	}
}

func TestInspect_EmptyGroupsPasses(t *testing.T) {
	p := firewall.RawPolicy{ID: "/sub/fw", Name: "fw"}
	got, _ := firewall.Inspect(context.Background(), &fakeARM{policies: []firewall.RawPolicy{p}}, "sub", fixedNow)
	if got[0].Result != firewall.ResultPass {
		t.Errorf("empty rule-collection set should PASS (no open management port); got %q", got[0].Result)
	}
}

// P0-614-2 (structural over-collection guard): the PolicyConfig /
// RuleCollectionGroup / RuleCollection / Rule / RawPolicy structs must carry
// rule CONFIGURATION ONLY — never flow logs, packet captures, traffic contents,
// NAT-rule secrets, threat-intel feeds, or route tables. This test reflects over
// every struct's field names and FAILS if any field name even hints at one of
// those excluded surfaces, so a future field that opens an over-collection door
// trips the build.
func TestStructs_RuleConfigOnly_NoTrafficSecretOrThreatIntelFields(t *testing.T) {
	banned := []string{
		"flowlog", "flow_log", "packet", "capture", "payload", "traffic",
		"content", "secret", "credential", "password", "token", "key",
		"pii", "body", "natsecret", "nat_secret", "threatintel", "threat_intel",
		"routetable", "route_table", "intel",
	}
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned over-collection token %q — rule-config-only struct must not carry flow-log/packet/traffic/NAT-secret/threat-intel/route-table data",
						typ.Name(), typ.Field(i).Name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(firewall.PolicyConfig{}))
	check(reflect.TypeOf(firewall.RuleCollectionGroup{}))
	check(reflect.TypeOf(firewall.RuleCollection{}))
	check(reflect.TypeOf(firewall.Rule{}))
	check(reflect.TypeOf(firewall.RawPolicy{}))
}

// TestRuleConfigOnly_PayloadFieldsPreserved pins that the documented
// rule-collection fields survive the Inspect transform (the positive companion
// to the structural guard).
func TestRuleConfigOnly_PayloadFieldsPreserved(t *testing.T) {
	api := &fakeARM{policies: []firewall.RawPolicy{segmented("/sub/fw", "fw")}}
	got, _ := firewall.Inspect(context.Background(), api, "sub", fixedNow)
	p := got[0]
	if len(p.RuleCollectionGroups) != 2 {
		t.Fatalf("groups not preserved: %+v", p.RuleCollectionGroups)
	}
	netGroup := p.RuleCollectionGroups[0]
	if netGroup.Name != "DefaultNetworkRuleCollectionGroup" || len(netGroup.RuleCollections) != 2 {
		t.Fatalf("network group not preserved: %+v", netGroup)
	}
	rc := netGroup.RuleCollections[0]
	if rc.Name != "allow-corp-ssh" || rc.Type != "network" || rc.Action != "allow" || rc.Priority != 100 {
		t.Errorf("rule collection not preserved: %+v", rc)
	}
	r := rc.Rules[0]
	if r.Name != "ssh-corp" || len(r.Protocols) != 1 || r.Protocols[0] != "tcp" ||
		r.SourceAddresses[0] != "203.0.113.0/24" || r.DestinationPorts[0] != "22" {
		t.Errorf("rule fields not preserved: %+v", r)
	}
	// Application rule's target FQDN preserved.
	appRule := p.RuleCollectionGroups[1].RuleCollections[0].Rules[0]
	if appRule.DestinationFQDNs[0] != "updates.example.com" {
		t.Errorf("application rule FQDN not preserved: %+v", appRule)
	}
}
