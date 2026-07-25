package nsg_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/nsg"
)

// fakeARM is a faked Azure Resource Manager surface — NO live Azure in tests.
type fakeARM struct {
	groups []nsg.RawGroup
	err    error
}

func (f *fakeARM) ListNetworkSecurityGroups(_ context.Context) ([]nsg.RawGroup, error) {
	return f.groups, f.err
}

var fixedNow = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

// segmented is a well-segmented NSG: SSH/RDP restricted to a corp prefix, plus a
// deny-all-inbound backstop.
func segmented(id, name string) nsg.RawGroup {
	return nsg.RawGroup{
		ID: id, Name: name, ResourceGroup: "rg", Location: "eastus",
		AssociatedSubnets: 1, AssociatedNICs: 0,
		Rules: []nsg.SecurityRule{
			{Name: "allow-ssh-corp", Direction: "inbound", Access: "allow", Protocol: "tcp",
				Priority: 100, SourceAddressPrefix: "203.0.113.0/24", DestinationPortRange: "22"},
			{Name: "deny-all-inbound", Direction: "inbound", Access: "deny", Protocol: "*",
				Priority: 4096, SourceAddressPrefix: "*", DestinationPortRange: "*"},
		},
	}
}

func TestInspect_PassWhenSegmented(t *testing.T) {
	api := &fakeARM{groups: []nsg.RawGroup{segmented("/sub/nsg1", "nsg1")}}
	got, err := nsg.Inspect(context.Background(), api, "sub-1", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 || got[0].Result != nsg.ResultPass {
		t.Fatalf("want 1 PASS; got %+v", got)
	}
	if got[0].SubscriptionID != "sub-1" {
		t.Errorf("subscription = %q; want sub-1", got[0].SubscriptionID)
	}
	if len(got[0].Rules) != 2 || got[0].AssociatedSubnets != 1 {
		t.Errorf("rule/association fields not preserved: %+v", got[0])
	}
}

func TestInspect_FailOnOpenManagementPort(t *testing.T) {
	cases := []struct {
		name string
		rule nsg.SecurityRule
	}{
		{"ssh-any-star", nsg.SecurityRule{Name: "bad-ssh", Direction: "inbound", Access: "allow", SourceAddressPrefix: "*", DestinationPortRange: "22"}},
		{"rdp-internet", nsg.SecurityRule{Name: "bad-rdp", Direction: "Inbound", Access: "Allow", SourceAddressPrefix: "Internet", DestinationPortRange: "3389"}},
		{"ssh-quad-zero", nsg.SecurityRule{Name: "bad-ssh2", Direction: "inbound", Access: "allow", SourceAddressPrefix: "0.0.0.0/0", DestinationPortRange: "22"}},
		{"any-port-range", nsg.SecurityRule{Name: "wide", Direction: "inbound", Access: "allow", SourceAddressPrefix: "*", DestinationPortRange: "0-65535"}},
		{"explicit-range", nsg.SecurityRule{Name: "range", Direction: "inbound", Access: "allow", SourceAddressPrefix: "any", DestinationPortRange: "20-25"}},
		{"star-port", nsg.SecurityRule{Name: "all-ports", Direction: "inbound", Access: "allow", SourceAddressPrefix: "*", DestinationPortRange: "*"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := nsg.RawGroup{ID: "/sub/nsg", Name: "nsg", Rules: []nsg.SecurityRule{tc.rule}}
			got, err := nsg.Inspect(context.Background(), &fakeARM{groups: []nsg.RawGroup{g}}, "sub", fixedNow)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if got[0].Result != nsg.ResultFail {
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
		name string
		rule nsg.SecurityRule
	}{
		{"corp-source", nsg.SecurityRule{Name: "ok-ssh", Direction: "inbound", Access: "allow", SourceAddressPrefix: "10.0.0.0/8", DestinationPortRange: "22"}},
		{"deny-from-any", nsg.SecurityRule{Name: "deny-ssh", Direction: "inbound", Access: "deny", SourceAddressPrefix: "*", DestinationPortRange: "22"}},
		{"outbound-any", nsg.SecurityRule{Name: "out-ssh", Direction: "outbound", Access: "allow", SourceAddressPrefix: "*", DestinationPortRange: "22"}},
		{"non-mgmt-port", nsg.SecurityRule{Name: "https", Direction: "inbound", Access: "allow", SourceAddressPrefix: "*", DestinationPortRange: "443"}},
		{"non-mgmt-range", nsg.SecurityRule{Name: "web", Direction: "inbound", Access: "allow", SourceAddressPrefix: "*", DestinationPortRange: "80-443"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := nsg.RawGroup{ID: "/sub/nsg", Name: "nsg", Rules: []nsg.SecurityRule{tc.rule}}
			got, _ := nsg.Inspect(context.Background(), &fakeARM{groups: []nsg.RawGroup{g}}, "sub", fixedNow)
			if got[0].Result != nsg.ResultPass {
				t.Errorf("result = %q; want pass (management port not Internet-exposed)", got[0].Result)
			}
		})
	}
}

func TestInspect_InconclusiveOnReadError(t *testing.T) {
	g := segmented("/sub/nsg", "nsg")
	g.ReadError = "throttled"
	got, err := nsg.Inspect(context.Background(), &fakeARM{groups: []nsg.RawGroup{g}}, "sub", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != nsg.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", got[0].Result)
	}
}

func TestInspect_SkipsIncompleteGroups(t *testing.T) {
	api := &fakeARM{groups: []nsg.RawGroup{
		{ID: "", Name: "x"},
		{ID: "y", Name: ""},
		segmented("/sub/ok", "ok"),
	}}
	got, _ := nsg.Inspect(context.Background(), api, "sub", fixedNow)
	if len(got) != 1 || got[0].NSGName != "ok" {
		t.Fatalf("expected 1 valid NSG; got %+v", got)
	}
}

func TestInspect_PropagatesListError(t *testing.T) {
	sentinel := errors.New("arm 403")
	_, err := nsg.Inspect(context.Background(), &fakeARM{err: sentinel}, "sub", fixedNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestInspect_NilAPIRejected(t *testing.T) {
	_, err := nsg.Inspect(context.Background(), nil, "sub", nil)
	if err == nil {
		t.Fatal("expected error for nil API")
	}
}

func TestInspect_EmptyRulesPasses(t *testing.T) {
	g := nsg.RawGroup{ID: "/sub/nsg", Name: "nsg"}
	got, _ := nsg.Inspect(context.Background(), &fakeARM{groups: []nsg.RawGroup{g}}, "sub", fixedNow)
	if got[0].Result != nsg.ResultPass {
		t.Errorf("empty rule set should PASS (no open management port); got %q", got[0].Result)
	}
}

// P0-520-2 (structural over-collection guard): the GroupConfig / SecurityRule /
// RawGroup structs must carry RULE METADATA ONLY — never flow logs, packet
// captures, traffic contents, secrets, or PII. This test reflects over every
// struct's field names and FAILS if any field name even hints at a
// flow-log / packet / traffic-content / secret surface, so a future field that
// opens an over-collection door trips the build.
func TestStructs_RulesOnly_NoTrafficOrSecretFields(t *testing.T) {
	banned := []string{
		"flowlog", "flow_log", "packet", "capture", "payload", "traffic",
		"content", "secret", "credential", "password", "token", "key",
		"pii", "body",
	}
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned over-collection token %q — rules-only struct must not carry flow-log/packet/traffic-content/secret data",
						typ.Name(), typ.Field(i).Name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(nsg.GroupConfig{}))
	check(reflect.TypeOf(nsg.SecurityRule{}))
	check(reflect.TypeOf(nsg.RawGroup{}))
}

// TestRulesOnly_PayloadFieldsPreserved pins that the documented rule fields
// survive the Inspect transform (the positive companion to the structural
// guard).
func TestRulesOnly_PayloadFieldsPreserved(t *testing.T) {
	api := &fakeARM{groups: []nsg.RawGroup{segmented("/sub/nsg", "nsg")}}
	got, _ := nsg.Inspect(context.Background(), api, "sub", fixedNow)
	g := got[0]
	if len(g.Rules) != 2 {
		t.Fatalf("rules not preserved: %+v", g.Rules)
	}
	r := g.Rules[0]
	if r.Name != "allow-ssh-corp" || r.Direction != "inbound" || r.Access != "allow" ||
		r.Protocol != "tcp" || r.Priority != 100 || r.SourceAddressPrefix != "203.0.113.0/24" ||
		r.DestinationPortRange != "22" {
		t.Errorf("rule fields not preserved: %+v", r)
	}
}
