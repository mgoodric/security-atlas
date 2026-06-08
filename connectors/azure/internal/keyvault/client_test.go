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

// --- slice 615: RBAC roleAssignments second read ---

// rbacVaultPage returns one RBAC-mode vault (no legacy access policies) and one
// legacy access-policy vault. NEUTRAL fixtures — obviously-fake ids only.
const rbacVaultPage = `{
	"value": [
		{
			"id": "/subscriptions/test-sub/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/kv-rbac",
			"name": "kv-rbac",
			"location": "eastus",
			"properties": {
				"enableRbacAuthorization": true,
				"enablePurgeProtection": true,
				"enableSoftDelete": true,
				"publicNetworkAccess": "Disabled",
				"networkAcls": { "defaultAction": "Deny" }
			}
		},
		{
			"id": "/subscriptions/test-sub/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/kv-legacy",
			"name": "kv-legacy",
			"location": "eastus",
			"properties": {
				"enableRbacAuthorization": false,
				"enablePurgeProtection": true,
				"enableSoftDelete": true,
				"accessPolicies": [
					{ "objectId": "00000000-0000-0000-0000-0000000000aa", "permissions": { "secrets": ["Get"] } }
				]
			}
		}
	]
}`

// armRouter serves a faked ARM management plane: the vaults list, the per-vault
// roleAssignments read, and the roleDefinitions name lookup. It records every
// path it saw so a test can assert the legacy vault triggered NO second read and
// that the data plane was never touched.
type armRouter struct {
	roleDefName    string
	roleAssignBody string // override per-vault roleAssignments body; default below
	roleAssignCode int    // override status for roleAssignments; default 200
	roleDefCode    int    // override status for roleDefinitions; default 200
	roleDefBody    string // override roleDefinitions body (e.g. bad json); default below
	mu             struct {
		paths []string
	}
}

func (a *armRouter) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.paths = append(a.mu.paths, r.URL.Path)
		switch {
		case strings.Contains(r.URL.Path, "Microsoft.Authorization/roleAssignments"):
			if a.roleAssignCode != 0 {
				w.WriteHeader(a.roleAssignCode)
				return
			}
			body := a.roleAssignBody
			if body == "" {
				body = `{"value":[
					{"properties":{"principalId":"00000000-0000-0000-0000-000000000001","roleDefinitionId":"/subscriptions/test-sub/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
					{"properties":{"principalId":"00000000-0000-0000-0000-000000000002","roleDefinitionId":"/subscriptions/test-sub/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}}
				]}`
			}
			_, _ = w.Write([]byte(body))
		case strings.Contains(r.URL.Path, "Microsoft.Authorization/roleDefinitions"):
			if a.roleDefCode != 0 {
				w.WriteHeader(a.roleDefCode)
				return
			}
			if a.roleDefBody != "" {
				_, _ = w.Write([]byte(a.roleDefBody))
				return
			}
			name := a.roleDefName
			if name == "" {
				name = "Key Vault Reader"
			}
			_, _ = w.Write([]byte(`{"properties":{"roleName":"` + name + `"}}`))
		default:
			_, _ = w.Write([]byte(rbacVaultPage))
		}
	}
}

func TestClient_ListVaults_RBACMergesRoleAssignments(t *testing.T) {
	router := &armRouter{}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}

	// kv-rbac: RBAC mode, two role assignments merged with resolved role name.
	rbac := got[0]
	if rbac.Name != "kv-rbac" || !rbac.RBACAuthorization {
		t.Fatalf("first vault should be the RBAC vault: %+v", rbac)
	}
	if rbac.ReadError != "" {
		t.Fatalf("RBAC vault read errored: %q", rbac.ReadError)
	}
	if len(rbac.AccessEntries) != 2 {
		t.Fatalf("rbac access entries = %d; want 2: %+v", len(rbac.AccessEntries), rbac.AccessEntries)
	}
	for _, e := range rbac.AccessEntries {
		if e.PrincipalType != "rbac_role_assignment" {
			t.Errorf("principal_type = %q; want rbac_role_assignment", e.PrincipalType)
		}
		if e.RoleName != "Key Vault Reader" {
			t.Errorf("role_name = %q; want resolved 'Key Vault Reader'", e.RoleName)
		}
		if len(e.Permissions) != 0 {
			t.Errorf("rbac entry must carry NO access-policy verbs: %+v", e.Permissions)
		}
	}
	if rbac.AccessEntries[0].PrincipalID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("principal id not preserved: %+v", rbac.AccessEntries[0])
	}

	// kv-legacy: access-policy mode — its access entries come from accessPolicies,
	// NOT from a second read.
	legacy := got[1]
	if legacy.Name != "kv-legacy" || legacy.RBACAuthorization {
		t.Fatalf("second vault should be the legacy vault: %+v", legacy)
	}
	if len(legacy.AccessEntries) != 1 || legacy.AccessEntries[0].PrincipalType != "access_policy" {
		t.Errorf("legacy vault access entries wrong: %+v", legacy.AccessEntries)
	}

	// The legacy vault MUST NOT have triggered a roleAssignments read (only the
	// RBAC vault's id should appear in a roleAssignments path); the data plane is
	// never touched.
	var sawLegacyRoleAssign bool
	for _, p := range router.mu.paths {
		if strings.Contains(p, "vault.azure.net") {
			t.Errorf("reached the Key-Vault DATA plane: %q (P0-615-2)", p)
		}
		if strings.Contains(p, "roleAssignments") && strings.Contains(p, "kv-legacy") {
			sawLegacyRoleAssign = true
		}
	}
	if sawLegacyRoleAssign {
		t.Error("legacy access-policy vault triggered a roleAssignments read — second read must be RBAC-only")
	}
}

func TestClient_ListVaults_RoleNameFallsBackToGUIDOnLookupFailure(t *testing.T) {
	// roleDefinitions lookup returns an empty name -> fall back to the bare guid.
	router := &armRouter{roleDefName: " "}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	e := got[0].AccessEntries[0]
	if e.RoleName != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("role_name = %q; want guid fallback", e.RoleName)
	}
}

func TestClient_ListVaults_RoleAssignmentReadErrorMarksInconclusive(t *testing.T) {
	// roleAssignments read 403s -> the vault is marked with a ReadError (the
	// collector renders that as INCONCLUSIVE) rather than dropping the vault or
	// failing the whole run.
	router := &armRouter{roleAssignCode: http.StatusForbidden}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults must not fail the run on a per-vault read error: %v", err)
	}
	if got[0].ReadError == "" {
		t.Error("RBAC vault with a failed roleAssignments read should carry a ReadError")
	}
	if len(got[0].AccessEntries) != 0 {
		t.Errorf("no role assignments should merge on read failure: %+v", got[0].AccessEntries)
	}
	// The other vault (legacy) is unaffected.
	if got[1].ReadError != "" {
		t.Errorf("legacy vault wrongly marked with ReadError: %q", got[1].ReadError)
	}
}

func TestClient_ListVaults_RoleAssignmentsTruncatedPerVault(t *testing.T) {
	// Build a roleAssignments page well above the per-vault cap; the connector
	// must truncate (bounded read, DoS guard) — pagination is a separate follow-on.
	var b strings.Builder
	b.WriteString(`{"value":[`)
	const n = 250 // > maxRoleAssignmentsPerVault (200)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"properties":{"principalId":"00000000-0000-0000-0000-0000000003","roleDefinitionId":"/x/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}}`)
	}
	b.WriteString(`]}`)
	router := &armRouter{roleAssignBody: b.String()}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(got[0].AccessEntries) != 200 {
		t.Errorf("rbac access entries = %d; want truncated to 200 (per-vault DoS cap)", len(got[0].AccessEntries))
	}
}

func TestClient_ListVaults_RoleAssignmentsBadJSONMarksInconclusive(t *testing.T) {
	router := &armRouter{roleAssignBody: `not json`}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults must not fail the run: %v", err)
	}
	if got[0].ReadError == "" {
		t.Error("bad roleAssignments json should mark the vault with a ReadError")
	}
}

func TestClient_ListVaults_RoleDefinitionLookupFailureFallsBackToGUID(t *testing.T) {
	// roleDefinitions endpoint 500s -> name resolution fails -> guid fallback.
	router := &armRouter{roleDefCode: http.StatusInternalServerError}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if got[0].AccessEntries[0].RoleName != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("role_name = %q; want guid fallback on roleDefinitions error", got[0].AccessEntries[0].RoleName)
	}
}

func TestClient_ListVaults_RoleDefinitionBadJSONFallsBackToGUID(t *testing.T) {
	router := &armRouter{roleDefBody: `not json`}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if got[0].AccessEntries[0].RoleName != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("role_name = %q; want guid fallback on bad roleDefinitions json", got[0].AccessEntries[0].RoleName)
	}
}

// TestClient_ListVaults_RoleNameCachedAcrossAssignments pins that the role-name
// resolution cache resolves a repeated roleDefinition guid exactly once: both
// assignments in the default fixture share a guid, so only ONE roleDefinitions
// lookup should hit the server.
func TestClient_ListVaults_RoleNameCachedAcrossAssignments(t *testing.T) {
	router := &armRouter{}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	if _, err := c.ListVaults(context.Background()); err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	var defLookups int
	for _, p := range router.mu.paths {
		if strings.Contains(p, "roleDefinitions") {
			defLookups++
		}
	}
	if defLookups != 1 {
		t.Errorf("roleDefinitions lookups = %d; want 1 (cached across assignments sharing a guid)", defLookups)
	}
}

// TestClient_ListVaults_EmptyRoleDefinitionID covers the resolveRoleName empty-id
// branch (an assignment with no roleDefinitionId yields an empty role name).
func TestClient_ListVaults_EmptyRoleDefinitionID(t *testing.T) {
	router := &armRouter{roleAssignBody: `{"value":[{"properties":{"principalId":"00000000-0000-0000-0000-000000000009","roleDefinitionId":""}}]}`}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(got[0].AccessEntries) != 1 || got[0].AccessEntries[0].RoleName != "" {
		t.Errorf("empty roleDefinitionId should yield empty role name: %+v", got[0].AccessEntries)
	}
}

// TestClient_ListVaults_SkipsAssignmentWithoutPrincipal covers the
// missing-principal skip branch in listVaultRoleAssignments.
func TestClient_ListVaults_SkipsAssignmentWithoutPrincipal(t *testing.T) {
	router := &armRouter{roleAssignBody: `{"value":[
		{"properties":{"principalId":"","roleDefinitionId":"/x/roleDefinitions/g"}},
		{"properties":{"principalId":"00000000-0000-0000-0000-000000000007","roleDefinitionId":"/x/roleDefinitions/g"}}
	]}`}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()
	c := keyvault.NewClient(srv.Client(), srv.URL, "test-sub", "tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(got[0].AccessEntries) != 1 {
		t.Errorf("assignment with empty principal should be skipped: %+v", got[0].AccessEntries)
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
