package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphConfigProfileTestServer stands up a fake Microsoft Graph that handles the
// token exchange and the managedDevices + deviceConfigurationStates read. NO
// live Graph.
func graphConfigProfileTestServer(t *testing.T, devicesJSON string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-graph-bearer","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/v1.0/deviceManagement/managedDevices"):
			if r.Method != http.MethodGet {
				t.Errorf("managedDevices method = %s; want GET (read-only)", r.Method)
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

func TestClient_ListConfigProfiles_DecodesMetadataOnly_NoSecrets(t *testing.T) {
	t.Parallel()
	// The payload deliberately includes fields the client MUST NOT decode: a raw
	// settingPayload / wifiPassword / certificate blob under each config state, and
	// owner detail under the device.
	payload := `{
	  "value": [
	    {
	      "id": "d-1", "deviceName": "ENG-PC-014", "userPrincipalName": "secret@fixture.invalid",
	      "deviceConfigurationStates": [
	        {"id": "cfg-1", "displayName": "Windows Compliance", "state": "compliant",
	         "settingPayload": "FAKE-SECRET-BLOB-FIXTURE", "wifiPassword": "hunter2", "certificate": "FAKE-CERT-MATERIAL-FIXTURE"},
	        {"id": "cfg-2", "displayName": "BitLocker Policy", "state": "compliant"},
	        {"id": "cfg-3", "displayName": "", "state": "error"}
	      ]
	    }
	  ]
	}`
	srv, log := graphConfigProfileTestServer(t, payload)
	c := NewClient(cfgFor(srv))
	got, err := c.ListConfigProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListConfigProfiles: %v", err)
	}
	if log.authHeader != "Bearer fake-graph-bearer" {
		t.Errorf("auth header = %q", log.authHeader)
	}
	// The read MUST request the deviceConfigurationStates expansion but never the
	// device name / owner contact detail.
	if !strings.Contains(log.rawQuery, "deviceConfigurationStates") {
		t.Errorf("query missing deviceConfigurationStates expansion: %q", log.rawQuery)
	}
	for _, banned := range []string{"deviceName", "userPrincipalName", "phoneNumber", "emailAddress"} {
		if strings.Contains(log.rawQuery, banned) {
			t.Errorf("query requested %q (P0-556): %q", banned, log.rawQuery)
		}
	}
	if len(got) != 1 {
		t.Fatalf("device len = %d; want 1", len(got))
	}
	profiles := got[0].Profiles
	// cfg-3 has an empty displayName and is dropped.
	if len(profiles) != 2 {
		t.Fatalf("profiles len = %d; want 2", len(profiles))
	}
	if profiles[0].Name != "Windows Compliance" || profiles[0].Identifier != "cfg-1" {
		t.Errorf("profile decode wrong: %+v", profiles[0])
	}
	// RawConfigProfile has no field for settingPayload / wifiPassword /
	// certificate, so they could not have been decoded. Settings is empty at v0.
	if len(profiles[0].Settings) != 0 {
		t.Errorf("Intune v0 settings should be empty (metadata-only): %+v", profiles[0].Settings)
	}
}

func TestClient_ListConfigProfiles_SkipsMissingDeviceID(t *testing.T) {
	t.Parallel()
	payload := `{"value":[{"id":"","deviceConfigurationStates":[{"id":"x","displayName":"X"}]},{"id":"keep","deviceConfigurationStates":[{"id":"y","displayName":"Y"}]}]}`
	srv, _ := graphConfigProfileTestServer(t, payload)
	c := NewClient(cfgFor(srv))
	got, err := c.ListConfigProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListConfigProfiles: %v", err)
	}
	if len(got) != 1 || got[0].DeviceID != "keep" {
		t.Fatalf("got %+v; want only keep", got)
	}
}

func TestClient_ListConfigProfiles_TokenError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{HTTP: srv.Client(), TokenURL: srv.URL + "/t/oauth2/v2.0/token", GraphBaseURL: srv.URL + "/v1.0", ClientID: "c", ClientSecret: "fake-secret"})
	if _, err := c.ListConfigProfiles(context.Background()); err == nil {
		t.Fatal("want token error")
	}
}

func TestClient_ListConfigProfiles_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{HTTP: srv.Client(), TokenURL: srv.URL + "/t/oauth2/v2.0/token", GraphBaseURL: srv.URL + "/v1.0", ClientID: "c", ClientSecret: "fake-secret"})
	if _, err := c.ListConfigProfiles(context.Background()); err == nil {
		t.Fatal("want HTTP error")
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
		{DeviceID: "", Profiles: []RawConfigProfile{{Name: "skip"}}},
		{DeviceID: "keep", Profiles: []RawConfigProfile{{
			Name: "Compliance", Identifier: "id", ProfileType: "configuration",
			Settings: []RawConfigSetting{{Key: "disk_encryption_enforced", Value: "true"}},
		}}},
	}}
	got, err := CollectConfigProfiles(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectConfigProfiles: %v", err)
	}
	if len(got) != 1 || got[0].DeviceID != "keep" {
		t.Fatalf("got %+v; want only keep", got)
	}
	if got[0].Profiles[0].Settings[0].Key != "disk_encryption_enforced" {
		t.Errorf("mapping wrong: %+v", got[0].Profiles[0])
	}
}

type fakeConfigProfileAPI struct {
	out []RawDeviceConfigProfiles
	err error
}

func (f fakeConfigProfileAPI) ListConfigProfiles(_ context.Context) ([]RawDeviceConfigProfiles, error) {
	return f.out, f.err
}
