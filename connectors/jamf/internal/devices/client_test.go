package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jamfTestServer stands up a fake Jamf Pro that handles the token exchange and
// the computers-inventory read. NO live Jamf.
func jamfTestServer(t *testing.T, inventoryJSON string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/oauth/token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s; want POST", r.Method)
			}
			_ = r.ParseForm()
			log.clientSecret = r.FormValue("client_secret")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-jamf-bearer","expires_in":1200}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/computers-inventory"):
			if r.Method != http.MethodGet {
				t.Errorf("inventory method = %s; want GET (read-only)", r.Method)
			}
			log.authHeader = r.Header.Get("Authorization")
			log.rawQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(inventoryJSON))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

type requestLog struct {
	clientSecret string
	authHeader   string
	rawQuery     string
}

func TestClient_ListComputers_DecodesPostureFields(t *testing.T) {
	t.Parallel()
	// The inventory payload deliberately includes sections the client MUST NOT
	// decode (applications, location GPS, owner email/phone).
	inv := `{
	  "totalCount": 1,
	  "results": [
	    {
	      "id": "501",
	      "general": {"name": "ENG-MBP-014", "supervised": true, "managed": true},
	      "operatingSystem": {"version": "14.5"},
	      "diskEncryption": {"fileVault2Status": "ENCRYPTED", "individualRecoveryKeyValidityStatus": "VALID"},
	      "security": {"screenLockGracePeriodEnforced": "ALWAYS", "gatekeeperStatus": "ENABLED"},
	      "userAndLocation": {"username": "u-501", "realname": "A. Engineer", "email": "secret.person@fixture.invalid", "phone": "555-FIXTURE", "buildingId": "bldg-1"},
	      "applications": [{"name": "SuperSecretApp", "version": "9"}],
	      "location": {"latitude": "37.77", "longitude": "-122.41"}
	    }
	  ]
	}`
	srv, log := jamfTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "client-1", "fake-jamf-secret")
	got, err := c.ListComputers(context.Background())
	if err != nil {
		t.Fatalf("ListComputers: %v", err)
	}
	if log.clientSecret != "fake-jamf-secret" {
		t.Errorf("token exchange did not send the secret form value")
	}
	if log.authHeader != "Bearer fake-jamf-bearer" {
		t.Errorf("inventory auth header = %q", log.authHeader)
	}
	// The $section query must NOT request APPLICATIONS (over-collection guard).
	if strings.Contains(log.rawQuery, "APPLICATIONS") {
		t.Errorf("query requested APPLICATIONS section (P0-490-3): %q", log.rawQuery)
	}
	if !strings.Contains(log.rawQuery, "DISK_ENCRYPTION") {
		t.Errorf("query missing DISK_ENCRYPTION section: %q", log.rawQuery)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	d := got[0]
	if d.ID != "501" || !d.FileVaultEnabled || !d.PasscodeCompliant || !d.Supervised {
		t.Errorf("posture decode wrong: %+v", d)
	}
	if d.OwnerAssignmentID != "u-501" || d.OwnerDisplayName != "A. Engineer" {
		t.Errorf("owner identity decode wrong: %+v", d)
	}
	// RawComputer has no field for email/phone/apps/GPS, so they could not have
	// been decoded — the struct shape is the guard.
}

func TestClient_ListComputers_FileVaultOffWhenNotEncrypted(t *testing.T) {
	t.Parallel()
	inv := `{"results":[{"id":"1","diskEncryption":{"fileVault2Status":"NOT_ENCRYPTED","individualRecoveryKeyValidityStatus":"NOT_APPLICABLE"},"security":{"screenLockGracePeriodEnforced":"NOT_ENFORCED"}}]}`
	srv, _ := jamfTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	got, err := c.ListComputers(context.Background())
	if err != nil {
		t.Fatalf("ListComputers: %v", err)
	}
	if got[0].FileVaultEnabled {
		t.Error("FileVault should be off")
	}
	if got[0].PasscodeCompliant {
		t.Error("screen-lock NOT_ENFORCED should be non-compliant")
	}
}

func TestClient_TokenExchangeError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	_, err := c.ListComputers(context.Background())
	if err == nil {
		t.Fatal("want token-exchange error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("want APIError 401; got %v", err)
	}
}

func TestClient_InventoryHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-jamf-bearer","expires_in":1200}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	_, err := c.ListComputers(context.Background())
	if err == nil {
		t.Fatal("want inventory HTTP error")
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://org.jamfcloud.com", "c", "s")
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
