package nsg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/nsg"
)

// neutralARMPage is a NEUTRAL fixture — no real subscription ids / secrets.
const neutralARMPage = `{
	"value": [
		{
			"id": "/subscriptions/test-sub/resourceGroups/rg1/providers/Microsoft.Network/networkSecurityGroups/nsg1",
			"name": "nsg1",
			"location": "eastus",
			"properties": {
				"securityRules": [
					{
						"name": "allow-https-internet",
						"properties": {
							"direction": "Inbound",
							"access": "Allow",
							"protocol": "Tcp",
							"priority": 100,
							"sourceAddressPrefix": "*",
							"destinationAddressPrefix": "*",
							"sourcePortRange": "*",
							"destinationPortRange": "443"
						}
					},
					{
						"name": "allow-ssh-corp",
						"properties": {
							"direction": "Inbound",
							"access": "Allow",
							"protocol": "Tcp",
							"priority": 110,
							"sourceAddressPrefix": "203.0.113.0/24",
							"destinationPortRange": "22"
						}
					}
				],
				"defaultSecurityRules": [
					{"name": "DenyAllInBound", "properties": {"direction": "Inbound", "access": "Deny"}}
				],
				"subnets": [{"id": "/subnets/s1"}, {"id": "/subnets/s2"}],
				"networkInterfaces": [{"id": "/nics/n1"}]
			}
		}
	]
}`

func TestClient_ListNetworkSecurityGroups_ParsesARMPage(t *testing.T) {
	var sawAuth, sawPath, sawMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		sawMethod = r.Method
		_, _ = w.Write([]byte(neutralARMPage))
	}))
	defer srv.Close()

	c := nsg.NewClient(srv.Client(), srv.URL, "test-sub", "test-access-token")
	got, err := c.ListNetworkSecurityGroups(context.Background())
	if err != nil {
		t.Fatalf("ListNetworkSecurityGroups: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	g := got[0]
	if g.Name != "nsg1" || g.ResourceGroup != "rg1" || g.Location != "eastus" {
		t.Errorf("nsg fields wrong: %+v", g)
	}
	// Only operator-authored securityRules are mapped; defaultSecurityRules are
	// deliberately dropped (platform boilerplate, no compliance signal).
	if len(g.Rules) != 2 {
		t.Fatalf("rules = %d; want 2 (defaultSecurityRules excluded)", len(g.Rules))
	}
	if g.AssociatedSubnets != 2 || g.AssociatedNICs != 1 {
		t.Errorf("association counts wrong: subnets=%d nics=%d", g.AssociatedSubnets, g.AssociatedNICs)
	}
	r0 := g.Rules[0]
	if r0.Name != "allow-https-internet" || r0.Direction != "inbound" || r0.Access != "allow" ||
		r0.Protocol != "tcp" || r0.Priority != 100 || r0.DestinationPortRange != "443" {
		t.Errorf("rule0 fields wrong: %+v", r0)
	}
	// P0-520-3: the connector must issue GET only — never mutate a network
	// resource.
	if sawMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only, P0-520-3)", sawMethod)
	}
	if sawAuth != "Bearer test-access-token" {
		t.Errorf("auth header = %q", sawAuth)
	}
	if !strings.Contains(sawPath, "/subscriptions/test-sub/") {
		t.Errorf("path = %q; want subscription-scoped", sawPath)
	}
	if !strings.Contains(sawPath, "networkSecurityGroups") {
		t.Errorf("path = %q; want networkSecurityGroups list endpoint", sawPath)
	}
}

func TestClient_ListNetworkSecurityGroups_EmptyRules(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[{"id":"/x","name":"x","properties":{}}]}`))
	}))
	defer srv.Close()
	c := nsg.NewClient(nil, srv.URL, "test-sub", "tok")
	got, err := c.ListNetworkSecurityGroups(context.Background())
	if err != nil {
		t.Fatalf("ListNetworkSecurityGroups: %v", err)
	}
	if len(got[0].Rules) != 0 || got[0].AssociatedSubnets != 0 || got[0].AssociatedNICs != 0 {
		t.Errorf("absent fields should default empty/zero: %+v", got[0])
	}
}

func TestClient_ListNetworkSecurityGroups_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := nsg.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListNetworkSecurityGroups(context.Background()); err == nil {
		t.Fatal("expected HTTP 403 error")
	}
}

func TestClient_ListNetworkSecurityGroups_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := nsg.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListNetworkSecurityGroups(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestNewClient_DefaultsBaseURL(t *testing.T) {
	c := nsg.NewClient(nil, "", "test-sub", "tok")
	if c.BaseURL != "https://management.azure.com" {
		t.Errorf("default base URL = %q", c.BaseURL)
	}
}

func TestAPIError_Message(t *testing.T) {
	if (&nsg.APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if (&nsg.APIError{Status: 403, Body: "denied"}).Error() == "" {
		t.Error("empty error message with body")
	}
}
