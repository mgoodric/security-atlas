package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jamfConfigProfileTestServer stands up a fake Jamf Pro that handles the token
// exchange and the computers-inventory config-profile read. NO live Jamf.
func jamfConfigProfileTestServer(t *testing.T, inventoryJSON string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/oauth/token":
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

func TestClient_ListConfigProfiles_DecodesMetadataOnly_NoSecrets(t *testing.T) {
	t.Parallel()
	// The payload deliberately includes fields the client MUST NOT decode: the raw
	// PayloadContent blob, a Wi-Fi PSK, and a certificate private key per profile.
	inv := `{
	  "results": [
	    {
	      "id": "501",
	      "general": {"name": "ENG-MBP-014", "supervised": true, "managed": true},
	      "configurationProfiles": [
	        {"displayName": "Passcode Policy", "profileIdentifier": "com.acme.passcode",
	         "uuid": "AAAA-BBBB", "lastInstalled": "2026-01-02T00:00:00Z",
	         "payloadContent": "<data>c2VjcmV0</data>", "wifiPassword": "hunter2",
	         "certificatePrivateKey": "FAKE-PRIVKEY-MATERIAL-FIXTURE"},
	        {"displayName": "FileVault Configuration", "profileIdentifier": "com.acme.fv2"}
	      ],
	      "diskEncryption": {"fileVault2Status": "ENCRYPTED"},
	      "security": {"gatekeeperStatus": "App Store and identified developers", "screenLockGracePeriodEnforced": "ALWAYS"}
	    }
	  ]
	}`
	srv, log := jamfConfigProfileTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "client-1", "fake-jamf-secret")
	got, err := c.ListConfigProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListConfigProfiles: %v", err)
	}
	if log.authHeader != "Bearer fake-jamf-bearer" {
		t.Errorf("auth header = %q", log.authHeader)
	}
	// The read MUST request the CONFIGURATION_PROFILES section but never the GPS
	// location, owner-contact, or APPLICATIONS sections.
	if !strings.Contains(log.rawQuery, "CONFIGURATION_PROFILES") {
		t.Errorf("query missing CONFIGURATION_PROFILES section: %q", log.rawQuery)
	}
	// The enrichment read also requests the posture sections (same read-only role,
	// no new scope) but NEVER the owner-contact / GPS / APPLICATIONS sections.
	for _, banned := range []string{"USER_AND_LOCATION", "LOCATION", "APPLICATIONS"} {
		if strings.Contains(log.rawQuery, banned) {
			t.Errorf("query requested %q section (P0-556): %q", banned, log.rawQuery)
		}
	}
	if len(got) != 1 {
		t.Fatalf("device len = %d; want 1", len(got))
	}
	profiles := got[0].Profiles
	// Two literal profiles + one synthetic "Enforced Configuration Summary" profile.
	if len(profiles) != 3 {
		t.Fatalf("profiles len = %d; want 3 (2 literal + 1 enforced summary)", len(profiles))
	}
	if profiles[0].Name != "Passcode Policy" || profiles[0].Identifier != "com.acme.passcode" ||
		profiles[0].UUID != "AAAA-BBBB" || profiles[0].LastModified != "2026-01-02T00:00:00Z" {
		t.Errorf("profile decode wrong: %+v", profiles[0])
	}
	// Literal profiles carry no settings (CONFIGURATION_PROFILES is metadata-only).
	if len(profiles[0].Settings) != 0 {
		t.Errorf("literal profile settings should be empty (metadata-only): %+v", profiles[0].Settings)
	}
	// The synthetic summary profile carries the enforced hardening facts.
	summary := profiles[len(profiles)-1]
	if summary.Name != enforcedSummaryProfileName {
		t.Fatalf("last profile = %q; want %q", summary.Name, enforcedSummaryProfileName)
	}
	want := map[string]string{
		"disk_encryption_enforced": "true",
		"gatekeeper_enabled":       "true",
		"screen_lock_enforced":     "true",
		"device_supervised":        "true",
		"device_managed":           "true",
	}
	if len(summary.Settings) != len(want) {
		t.Fatalf("summary settings = %+v; want %v", summary.Settings, want)
	}
	for _, s := range summary.Settings {
		if want[s.Key] != s.Value {
			t.Errorf("summary setting %q = %q; want %q", s.Key, s.Value, want[s.Key])
		}
	}
	// RawConfigProfile has no field for payloadContent / wifiPassword /
	// certificatePrivateKey, so they could not have been decoded — the struct shape
	// is the first guard.
}

// TestClient_ListConfigProfiles_NoSecretValueLeaks is the load-bearing
// secret-drop guard at the Jamf client: even with a payload that places fake
// secret values right next to the posture fields, no RawConfigSetting value ever
// equals a secret marker — the enrichment derives ONLY booleans from
// enforced-state fields, never copies a payload value.
func TestClient_ListConfigProfiles_NoSecretValueLeaks(t *testing.T) {
	t.Parallel()
	inv := `{
	  "results": [
	    {
	      "id": "777",
	      "general": {"supervised": false, "managed": true},
	      "configurationProfiles": [
	        {"displayName": "WiFi-Corp", "profileIdentifier": "com.acme.wifi",
	         "payloadContent": "<data>FAKE-SECRET-BLOB</data>", "wifiPassword": "FAKE-PSK-FIXTURE",
	         "vpnSharedSecret": "FAKE-SHARED-SECRET", "certificatePrivateKey": "FAKE-PRIVKEY"}
	      ],
	      "diskEncryption": {"fileVault2Status": "ENCRYPTED"},
	      "security": {"gatekeeperStatus": "Anywhere", "screenLockGracePeriodEnforced": "NOT_ENFORCED"}
	    }
	  ]
	}`
	srv, _ := jamfConfigProfileTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	got, err := c.ListConfigProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListConfigProfiles: %v", err)
	}
	secrets := []string{"FAKE-SECRET-BLOB", "FAKE-PSK-FIXTURE", "FAKE-SHARED-SECRET", "FAKE-PRIVKEY"}
	for _, dev := range got {
		for _, p := range dev.Profiles {
			for _, s := range p.Settings {
				for _, secret := range secrets {
					if strings.Contains(s.Value, secret) {
						t.Fatalf("secret value %q leaked into setting %q", secret, s.Key)
					}
				}
			}
		}
	}
	// The enforced summary reflects the disabled state honestly (Gatekeeper off,
	// screen-lock not enforced, not supervised).
	summary := got[0].Profiles[len(got[0].Profiles)-1]
	want := map[string]string{
		"disk_encryption_enforced": "true",
		"gatekeeper_enabled":       "false",
		"screen_lock_enforced":     "false",
		"device_supervised":        "false",
		"device_managed":           "true",
	}
	for _, s := range summary.Settings {
		if want[s.Key] != s.Value {
			t.Errorf("summary setting %q = %q; want %q", s.Key, s.Value, want[s.Key])
		}
	}
}

func TestClient_ListConfigProfiles_SkipsMissingComputerID(t *testing.T) {
	t.Parallel()
	inv := `{"results":[{"id":"","configurationProfiles":[{"displayName":"X"}]},{"id":"keep","configurationProfiles":[{"displayName":"Y"}]}]}`
	srv, _ := jamfConfigProfileTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	got, err := c.ListConfigProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListConfigProfiles: %v", err)
	}
	if len(got) != 1 || got[0].ComputerID != "keep" {
		t.Fatalf("got %+v; want only keep", got)
	}
}

func TestClient_ListConfigProfiles_TokenError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	if _, err := c.ListConfigProfiles(context.Background()); err == nil {
		t.Fatal("want token error")
	}
}

func TestClient_ListConfigProfiles_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":1200}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	if _, err := c.ListConfigProfiles(context.Background()); err == nil {
		t.Fatal("want inventory HTTP error")
	}
}

func TestCollectConfigProfiles_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := CollectConfigProfiles(context.Background(), nil); err == nil {
		t.Fatal("want error on nil api")
	}
}

func TestCollectConfigProfiles_MapsAndDropsEmptyIDs(t *testing.T) {
	t.Parallel()
	api := fakeConfigProfileAPI{out: []RawDeviceConfigProfiles{
		{ComputerID: "", Profiles: []RawConfigProfile{{Name: "skip"}}},
		{ComputerID: "keep", Profiles: []RawConfigProfile{{
			Name: "Passcode", Identifier: "id", ProfileType: "configuration",
			Scope: []string{"All"}, UUID: "u", LastModified: "t",
			Settings: []RawConfigSetting{{Key: "passcode_required", Value: "true"}},
		}}},
	}}
	got, err := CollectConfigProfiles(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectConfigProfiles: %v", err)
	}
	if len(got) != 1 || got[0].DeviceID != "keep" {
		t.Fatalf("got %+v; want only keep", got)
	}
	p := got[0].Profiles[0]
	if p.Name != "Passcode" || p.Settings[0].Key != "passcode_required" {
		t.Errorf("mapping wrong: %+v", p)
	}
}

type fakeConfigProfileAPI struct {
	out []RawDeviceConfigProfiles
	err error
}

func (f fakeConfigProfileAPI) ListConfigProfiles(_ context.Context) ([]RawDeviceConfigProfiles, error) {
	return f.out, f.err
}
