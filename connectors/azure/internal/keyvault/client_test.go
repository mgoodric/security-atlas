package keyvault_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/keyvault"
)

// neutralARMPage is a NEUTRAL fixture — no real subscription ids / secrets. The
// accessPolicies carry permission VERBS only (never a secret value); the
// payload deliberately contains NO secret/key/certificate material.
const neutralARMPage = `{
	"value": [
		{
			"id": "/subscriptions/test-sub/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/kv1",
			"name": "kv1",
			"location": "eastus",
			"properties": {
				"enableRbacAuthorization": false,
				"enablePurgeProtection": true,
				"enableSoftDelete": true,
				"publicNetworkAccess": "Disabled",
				"networkAcls": { "defaultAction": "Deny" },
				"accessPolicies": [
					{
						"objectId": "00000000-0000-0000-0000-000000000abc",
						"permissions": {
							"keys": ["Get", "List"],
							"secrets": ["Get"],
							"certificates": ["List"]
						}
					}
				]
			}
		},
		{
			"id": "/subscriptions/test-sub/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/kv-rbac",
			"name": "kv-rbac",
			"location": "eastus",
			"properties": {
				"enableRbacAuthorization": true,
				"enablePurgeProtection": true,
				"enableSoftDelete": true,
				"publicNetworkAccess": "Enabled"
			}
		}
	]
}`

func TestClient_ListVaults_ParsesARMPage(t *testing.T) {
	var sawAuth, sawPath, sawMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		sawMethod = r.Method
		_, _ = w.Write([]byte(neutralARMPage))
	}))
	defer srv.Close()

	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "test-access-token")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}

	// Legacy access-policy vault.
	v0 := got[0]
	if v0.Name != "kv1" || v0.ResourceGroup != "rg1" || v0.Location != "eastus" {
		t.Errorf("vault fields wrong: %+v", v0)
	}
	if v0.RBACAuthorization || !v0.PurgeProtection || !v0.SoftDeleteEnabled {
		t.Errorf("config flags wrong: %+v", v0)
	}
	if v0.PublicNetworkAccess != "Disabled" || v0.NetworkACLDefault != "Deny" {
		t.Errorf("network posture wrong: %+v", v0)
	}
	if len(v0.AccessEntries) != 1 {
		t.Fatalf("access entries = %d; want 1", len(v0.AccessEntries))
	}
	e := v0.AccessEntries[0]
	if e.PrincipalID != "00000000-0000-0000-0000-000000000abc" || e.PrincipalType != "access_policy" {
		t.Errorf("access entry principal wrong: %+v", e)
	}
	perms := strings.Join(e.Permissions, ",")
	for _, want := range []string{"keys:get", "keys:list", "secrets:get", "certificates:list"} {
		if !strings.Contains(perms, want) {
			t.Errorf("permissions %q missing %q", perms, want)
		}
	}

	// RBAC-mode vault: no legacy access policies, RBAC flag set.
	v1 := got[1]
	if !v1.RBACAuthorization || len(v1.AccessEntries) != 0 {
		t.Errorf("rbac vault should have no access policies: %+v", v1)
	}
	if v1.PublicNetworkAccess != "Enabled" || v1.NetworkACLDefault != "" {
		t.Errorf("rbac vault network posture wrong: %+v", v1)
	}

	// Management-plane read-only contract: GET only, never a data-plane call,
	// never a mutate.
	if sawMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only management plane, P0-521-2)", sawMethod)
	}
	if sawAuth != "Bearer test-access-token" {
		t.Errorf("auth header = %q", sawAuth)
	}
	if !strings.Contains(sawPath, "/subscriptions/test-sub/") {
		t.Errorf("path = %q; want subscription-scoped", sawPath)
	}
	if !strings.Contains(sawPath, "Microsoft.KeyVault/vaults") {
		t.Errorf("path = %q; want Microsoft.KeyVault/vaults list endpoint", sawPath)
	}
	// The connector must never reach the data plane (vault.azure.net).
	if strings.Contains(sawPath, "vault.azure.net") {
		t.Errorf("path = %q; reached the Key-Vault DATA plane (P0-521-2 violation)", sawPath)
	}
}

func TestClient_ListVaults_EmptyProperties(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[{"id":"/x","name":"x","properties":{}}]}`))
	}))
	defer srv.Close()
	c := keyvault.NewClient(nil, srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	v := got[0]
	if v.RBACAuthorization || v.PurgeProtection || v.SoftDeleteEnabled ||
		v.PublicNetworkAccess != "" || v.NetworkACLDefault != "" || len(v.AccessEntries) != 0 {
		t.Errorf("absent fields should default empty/false: %+v", v)
	}
}

func TestClient_ListVaults_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListVaults(context.Background()); err == nil {
		t.Fatal("expected HTTP 403 error")
	}
}

func TestClient_ListVaults_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListVaults(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestNewClient_DefaultsBaseURL(t *testing.T) {
	c := keyvault.NewClient(nil, "", "test-sub", "tok")
	if c.BaseURL != "https://management.azure.com" {
		t.Errorf("default base URL = %q", c.BaseURL)
	}
}

func TestAPIError_Message(t *testing.T) {
	if (&keyvault.APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if (&keyvault.APIError{Status: 403, Body: "denied"}).Error() == "" {
		t.Error("empty error message with body")
	}
}
