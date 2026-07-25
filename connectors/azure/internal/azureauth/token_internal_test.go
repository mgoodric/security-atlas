package azureauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withTokenEndpoint(t *testing.T, base string) {
	t.Helper()
	prev := tokenEndpointBase
	tokenEndpointBase = base
	t.Cleanup(func() { tokenEndpointBase = prev })
}

func TestAcquireToken_ClientCredentialsSuccess(t *testing.T) {
	const secret = "test-azure-client-secret"
	var sawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sawBody = r.Form.Encode()
		if r.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		_, _ = w.Write([]byte(`{"access_token":"test-graph-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	cred := Credential{mode: ModeClientCredentials, tenantID: "tenant-1", clientID: "client-1", secret: secret}
	tok, err := cred.AcquireToken(context.Background(), srv.Client(), "https://graph.microsoft.com/.default")
	if err != nil {
		t.Fatalf("AcquireToken: %v", err)
	}
	if tok != "test-graph-token" {
		t.Errorf("token = %q", tok)
	}
	// The request carries the secret (to Azure, source-side) but the secret
	// must not surface in any error we return — covered by the failure test.
	if !strings.Contains(sawBody, "client_credentials") {
		t.Errorf("body missing grant: %q", sawBody)
	}
}

func TestAcquireToken_HTTPErrorDoesNotLeakSecret(t *testing.T) {
	const secret = "test-azure-client-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	cred := Credential{mode: ModeClientCredentials, tenantID: "t", clientID: "c", secret: secret}
	_, err := cred.AcquireToken(context.Background(), srv.Client(), "scope")
	if err == nil {
		t.Fatal("expected token error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("token error leaked the secret: %v", err)
	}
}

func TestAcquireToken_EmptyAccessTokenRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"","token_type":"Bearer"}`))
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)
	cred := Credential{mode: ModeClientCredentials, tenantID: "t", clientID: "c", secret: "s"}
	if _, err := cred.AcquireToken(context.Background(), srv.Client(), "scope"); err == nil {
		t.Fatal("expected empty access_token error")
	}
}

func TestAcquireToken_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)
	cred := Credential{mode: ModeClientCredentials, tenantID: "t", clientID: "c", secret: "s"}
	if _, err := cred.AcquireToken(context.Background(), srv.Client(), "scope"); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestAcquireToken_ManagedIdentityNotWiredV0(t *testing.T) {
	cred := Credential{mode: ModeManagedIdentity, tenantID: "t"}
	_, err := cred.AcquireToken(context.Background(), nil, "scope")
	if err == nil || !strings.Contains(err.Error(), "managed-identity") {
		t.Fatalf("want managed-identity not-wired error; got %v", err)
	}
}

func TestAcquireToken_UnknownMode(t *testing.T) {
	cred := Credential{mode: AuthMode("bogus"), tenantID: "t"}
	if _, err := cred.AcquireToken(context.Background(), nil, "scope"); err == nil {
		t.Fatal("expected unknown-mode error")
	}
}
