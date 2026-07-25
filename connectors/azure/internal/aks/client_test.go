package aks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/aks"
)

// neutralARMPage is a NEUTRAL fixture — no real subscription ids / secrets.
const neutralARMPage = `{
	"value": [
		{
			"id": "/subscriptions/test-sub/resourceGroups/rg1/providers/Microsoft.ContainerService/managedClusters/cluster1",
			"name": "cluster1",
			"location": "eastus",
			"identity": {"type": "SystemAssigned"},
			"properties": {
				"kubernetesVersion": "1.29.2",
				"enableRBAC": true,
				"disableLocalAccounts": true,
				"networkProfile": {"networkPolicy": "calico"},
				"apiServerAccessProfile": {
					"enablePrivateCluster": true,
					"authorizedIPRanges": ["203.0.113.0/24"]
				},
				"oidcIssuerProfile": {"enabled": true},
				"agentPoolProfiles": [{"name": "system"}, {"name": "user"}]
			}
		}
	]
}`

func TestClient_ListManagedClusters_ParsesARMPage(t *testing.T) {
	var sawAuth, sawPath, sawMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		sawMethod = r.Method
		// P0-519-1: the connector must NEVER call listClusterAdminCredential
		// (or any *Credential POST) — that returns admin kubeconfig.
		if strings.Contains(strings.ToLower(r.URL.Path), "credential") {
			t.Errorf("connector hit a credential endpoint %q — privilege escalation (P0-519-1)", r.URL.Path)
		}
		_, _ = w.Write([]byte(neutralARMPage))
	}))
	defer srv.Close()

	c := aks.NewClient(srv.Client(), srv.URL, "test-sub", "test-access-token")
	got, err := c.ListManagedClusters(context.Background())
	if err != nil {
		t.Fatalf("ListManagedClusters: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	m := got[0]
	if m.Name != "cluster1" || m.ResourceGroup != "rg1" || m.Location != "eastus" {
		t.Errorf("cluster fields wrong: %+v", m)
	}
	if m.KubernetesVersion != "1.29.2" || !m.RBACEnabled || m.NetworkPolicy != "calico" {
		t.Errorf("core config fields wrong: %+v", m)
	}
	if !m.PrivateCluster || !m.AuthorizedIPRanges || !m.ManagedIdentity ||
		!m.LocalAccountsDisabled || !m.OIDCIssuerEnabled {
		t.Errorf("hardening flags wrong: %+v", m)
	}
	if m.NodePoolCount != 2 {
		t.Errorf("node pool count = %d; want 2", m.NodePoolCount)
	}
	if sawMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only, P0-519-1)", sawMethod)
	}
	if sawAuth != "Bearer test-access-token" {
		t.Errorf("auth header = %q", sawAuth)
	}
	if !strings.Contains(sawPath, "/subscriptions/test-sub/") {
		t.Errorf("path = %q; want subscription-scoped", sawPath)
	}
	if !strings.Contains(sawPath, "managedClusters") {
		t.Errorf("path = %q; want managedClusters list endpoint", sawPath)
	}
}

func TestClient_ListManagedClusters_NoIdentityMeansServicePrincipal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[{"id":"/x","name":"x","identity":{"type":"None"},"properties":{}}]}`))
	}))
	defer srv.Close()
	c := aks.NewClient(nil, srv.URL, "test-sub", "tok")
	got, err := c.ListManagedClusters(context.Background())
	if err != nil {
		t.Fatalf("ListManagedClusters: %v", err)
	}
	if got[0].ManagedIdentity {
		t.Error(`identity.type "None" should mean managed identity not reported`)
	}
	if got[0].PrivateCluster || got[0].AuthorizedIPRanges || got[0].OIDCIssuerEnabled {
		t.Errorf("absent profiles should default false: %+v", got[0])
	}
}

func TestClient_ListManagedClusters_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := aks.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListManagedClusters(context.Background()); err == nil {
		t.Fatal("expected HTTP 403 error")
	}
}

func TestClient_ListManagedClusters_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := aks.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListManagedClusters(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestNewClient_DefaultsBaseURL(t *testing.T) {
	c := aks.NewClient(nil, "", "test-sub", "tok")
	if c.BaseURL != "https://management.azure.com" {
		t.Errorf("default base URL = %q", c.BaseURL)
	}
}

func TestAPIError_Message(t *testing.T) {
	if (&aks.APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if (&aks.APIError{Status: 403, Body: "denied"}).Error() == "" {
		t.Error("empty error message with body")
	}
}
