package entra_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
)

func TestClient_ListRoleAssignments_ParsesGraphPage(t *testing.T) {
	var sawAuth, sawAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawAccept = r.Header.Get("Accept")
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		_, _ = w.Write([]byte(`{
			"value": [
				{
					"id": "ra-1",
					"principalId": "p-1",
					"roleDefinitionId": "role-1",
					"directoryScopeId": "/",
					"principal": {"@odata.type": "#microsoft.graph.user", "displayName": "Alice"},
					"roleDefinition": {"displayName": "Global Administrator"}
				},
				{
					"id": "ra-2",
					"principalId": "sp-1",
					"roleDefinitionId": "role-2",
					"directoryScopeId": "/administrativeUnits/au1",
					"principal": {"@odata.type": "#microsoft.graph.servicePrincipal", "displayName": "ci-bot"},
					"roleDefinition": {"displayName": "Reader"}
				}
			]
		}`))
	}))
	defer srv.Close()

	c := entra.NewClient(srv.Client(), srv.URL, "test-access-token")
	got, err := c.ListRoleAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListRoleAssignments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].PrincipalType != entra.PrincipalUser {
		t.Errorf("got[0] principal_type = %q; want user", got[0].PrincipalType)
	}
	if got[1].PrincipalType != entra.PrincipalServicePrincipal {
		t.Errorf("got[1] principal_type = %q; want servicePrincipal", got[1].PrincipalType)
	}
	if got[0].RoleDisplayName != "Global Administrator" {
		t.Errorf("role = %q", got[0].RoleDisplayName)
	}
	if sawAuth != "Bearer test-access-token" {
		t.Errorf("auth header = %q; want Bearer test-access-token", sawAuth)
	}
	if sawAccept != "application/json" {
		t.Errorf("accept = %q", sawAccept)
	}
}

func TestClient_ListRoleAssignments_MapsGroupAndUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[
			{"id":"g","principalId":"grp","roleDefinitionId":"r","principal":{"@odata.type":"#microsoft.graph.group"},"roleDefinition":{}},
			{"id":"d","principalId":"dev","roleDefinitionId":"r","principal":{"@odata.type":"#microsoft.graph.device"},"roleDefinition":{}}
		]}`))
	}))
	defer srv.Close()
	c := entra.NewClient(nil, srv.URL, "tok")
	got, err := c.ListRoleAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListRoleAssignments: %v", err)
	}
	if got[0].PrincipalType != entra.PrincipalGroup {
		t.Errorf("group type = %q", got[0].PrincipalType)
	}
	if got[1].PrincipalType != entra.PrincipalUnknown {
		t.Errorf("device → unknown; got %q", got[1].PrincipalType)
	}
}

func TestClient_ListRoleAssignments_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Authorization_RequestDenied"}`))
	}))
	defer srv.Close()
	c := entra.NewClient(srv.Client(), srv.URL, "tok")
	_, err := c.ListRoleAssignments(context.Background())
	if err == nil {
		t.Fatal("expected HTTP 403 error")
	}
	var apiErr *entra.APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Errorf("err = %v; want APIError 403", err)
	}
}

func TestClient_ListRoleAssignments_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := entra.NewClient(srv.Client(), srv.URL, "tok")
	if _, err := c.ListRoleAssignments(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestNewClient_DefaultsBaseURL(t *testing.T) {
	c := entra.NewClient(nil, "", "tok")
	if c.BaseURL != "https://graph.microsoft.com/v1.0" {
		t.Errorf("default base URL = %q", c.BaseURL)
	}
}

func TestAPIError_Message(t *testing.T) {
	e := &entra.APIError{Status: 500}
	if e.Error() == "" {
		t.Error("empty error message")
	}
	e2 := &entra.APIError{Status: 403, Body: "denied"}
	if e2.Error() == "" {
		t.Error("empty error message with body")
	}
}

// asAPIError is a tiny errors.As helper kept local to the test to avoid
// importing errors at file scope for one call.
func asAPIError(err error, target **entra.APIError) bool {
	for err != nil {
		if ae, ok := err.(*entra.APIError); ok {
			*target = ae
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
