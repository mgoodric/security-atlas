package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type requestLog struct {
	clientSecret string
	authHeader   string
	rawQuery     string
}

// graphTestServer stands up a fake identity-platform token endpoint + Graph
// managed-devices endpoint. NO live Graph.
func graphTestServer(t *testing.T, devicesJSON string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s; want POST", r.Method)
			}
			_ = r.ParseForm()
			log.clientSecret = r.FormValue("client_secret")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-graph-bearer","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/v1.0/deviceManagement/managedDevices"):
			if r.Method != http.MethodGet {
				t.Errorf("devices method = %s; want GET (read-only)", r.Method)
			}
			log.authHeader = r.Header.Get("Authorization")
			log.rawQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(devicesJSON))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func cfgFor(srv *httptest.Server) ClientConfig {
	return ClientConfig{
		HTTP:         srv.Client(),
		TokenURL:     srv.URL + "/tenant-1/oauth2/v2.0/token",
		GraphBaseURL: srv.URL + "/v1.0",
		Scope:        "https://graph.microsoft.com/.default",
		ClientID:     "client-1",
		ClientSecret: "fake-graph-secret",
	}
}

func TestClient_ListManagedDevices_DecodesPostureFields(t *testing.T) {
	t.Parallel()
	// The payload deliberately includes properties the client MUST NOT decode
	// (phoneNumber, emailAddress, a detectedApps inventory).
	payload := `{
	  "value": [
	    {
	      "id": "d-1",
	      "deviceName": "ENG-PC-014",
	      "osVersion": "10.0.22631",
	      "operatingSystem": "Windows",
	      "isEncrypted": true,
	      "complianceState": "compliant",
	      "managementAgent": "mdm",
	      "userPrincipalName": "u-1@tenant.example",
	      "userDisplayName": "A. Engineer",
	      "phoneNumber": "555-FIXTURE",
	      "emailAddress": "secret.person@fixture.invalid",
	      "detectedApps": [{"displayName": "SuperSecretApp"}]
	    }
	  ]
	}`
	srv, log := graphTestServer(t, payload)
	c := NewClient(cfgFor(srv))
	got, err := c.ListManagedDevices(context.Background())
	if err != nil {
		t.Fatalf("ListManagedDevices: %v", err)
	}
	if log.clientSecret != "fake-graph-secret" {
		t.Errorf("token exchange did not send the secret form value")
	}
	if log.authHeader != "Bearer fake-graph-bearer" {
		t.Errorf("devices auth header = %q", log.authHeader)
	}
	// The $select must NOT request detectedApps / phoneNumber / emailAddress.
	for _, banned := range []string{"detectedApps", "phoneNumber", "emailAddress"} {
		if strings.Contains(log.rawQuery, banned) {
			t.Errorf("$select requested %q (P0-490-3): %q", banned, log.rawQuery)
		}
	}
	if !strings.Contains(log.rawQuery, "isEncrypted") || !strings.Contains(log.rawQuery, "complianceState") {
		t.Errorf("$select missing posture fields: %q", log.rawQuery)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	d := got[0]
	if d.ID != "d-1" || !d.Encrypted || !d.PasscodeCompliant || d.OS != "Windows" {
		t.Errorf("posture decode wrong: %+v", d)
	}
	if d.OwnerAssignmentID != "u-1@tenant.example" || d.OwnerDisplayName != "A. Engineer" {
		t.Errorf("owner identity decode wrong: %+v", d)
	}
	// RawDevice has no field for phoneNumber/emailAddress/detectedApps, so they
	// could not have been decoded — the struct shape is the guard.
}

func TestClient_NonCompliantPasscodeNotPassed(t *testing.T) {
	t.Parallel()
	payload := `{"value":[{"id":"1","complianceState":"noncompliant","isEncrypted":false}]}`
	srv, _ := graphTestServer(t, payload)
	c := NewClient(cfgFor(srv))
	got, err := c.ListManagedDevices(context.Background())
	if err != nil {
		t.Fatalf("ListManagedDevices: %v", err)
	}
	if got[0].PasscodeCompliant {
		t.Error("noncompliant device should not be passcode-compliant")
	}
}

func TestClient_TokenExchangeError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{HTTP: srv.Client(), TokenURL: srv.URL + "/t/oauth2/v2.0/token", GraphBaseURL: srv.URL + "/v1.0", ClientID: "c", ClientSecret: "fake-secret"})
	_, err := c.ListManagedDevices(context.Background())
	if err == nil {
		t.Fatal("want token-exchange error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("want APIError 401; got %v", err)
	}
}

func TestClient_DevicesHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-graph-bearer","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{HTTP: srv.Client(), TokenURL: srv.URL + "/t/oauth2/v2.0/token", GraphBaseURL: srv.URL + "/v1.0", ClientID: "c", ClientSecret: "fake-secret"})
	_, err := c.ListManagedDevices(context.Background())
	if err == nil {
		t.Fatal("want devices HTTP error")
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(ClientConfig{GraphBaseURL: "https://graph.microsoft.com/v1.0", ClientID: "c", ClientSecret: "s"})
	if c.HTTP == nil {
		t.Error("default HTTP client not set")
	}
}

func TestAPIError_Message(t *testing.T) {
	t.Parallel()
	if (&APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if !strings.Contains((&APIError{Status: 503, Body: "boom"}).Error(), "boom") {
		t.Error("error should include body")
	}
}

func asAPIError(err error, target **APIError) bool {
	if e, ok := err.(*APIError); ok {
		*target = e
		return true
	}
	return false
}
