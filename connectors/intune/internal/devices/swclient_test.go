package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphSoftwareTestServer stands up a fake token endpoint + Graph detectedApps
// endpoint. NO live Graph.
func graphSoftwareTestServer(t *testing.T, appsJSON string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-graph-bearer","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/v1.0/deviceManagement/detectedApps"):
			if r.Method != http.MethodGet {
				t.Errorf("detectedApps method = %s; want GET (read-only)", r.Method)
			}
			log.authHeader = r.Header.Get("Authorization")
			log.rawQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(appsJSON))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func TestClient_ListDetectedApps_InvertsToDeviceCentricSoftwareOnly(t *testing.T) {
	t.Parallel()
	// The payload deliberately includes properties the client MUST NOT decode:
	// per-app sizeInByte, and per-device deviceName / userPrincipalName under the
	// managedDevices expansion.
	payload := `{
	  "value": [
	    {"id": "app-1", "displayName": "Google Chrome", "version": "125.0.6422.142", "sizeInByte": 540000000,
	     "managedDevices": [{"id": "d-1", "deviceName": "ENG-PC-014", "userPrincipalName": "secret@fixture.invalid"},
	                        {"id": "d-2"}]},
	    {"id": "app-2", "displayName": "openssl", "version": "3.0.13", "managedDevices": [{"id": "d-1"}]},
	    {"id": "app-3", "displayName": "", "managedDevices": [{"id": "d-1"}]}
	  ]
	}`
	srv, log := graphSoftwareTestServer(t, payload)
	c := NewClient(cfgFor(srv))
	got, err := c.ListDetectedApps(context.Background())
	if err != nil {
		t.Fatalf("ListDetectedApps: %v", err)
	}
	if log.authHeader != "Bearer fake-graph-bearer" {
		t.Errorf("auth header = %q", log.authHeader)
	}
	// The $select must NOT request sizeInByte; the $expand must $select the
	// device id only (never deviceName / userPrincipalName).
	for _, banned := range []string{"sizeInByte", "deviceName", "userPrincipalName"} {
		if strings.Contains(log.rawQuery, banned) {
			t.Errorf("query requested %q (P0-555): %q", banned, log.rawQuery)
		}
	}
	if !strings.Contains(log.rawQuery, "displayName") || !strings.Contains(log.rawQuery, "version") {
		t.Errorf("$select missing software fields: %q", log.rawQuery)
	}

	// Inverted device-centric: d-1 has Chrome + openssl (first-seen order), d-2
	// has Chrome only. The empty-name app is dropped.
	byID := map[string][]RawSoftwareItem{}
	for _, d := range got {
		byID[d.DeviceID] = d.Apps
	}
	if len(byID["d-1"]) != 2 {
		t.Errorf("d-1 apps = %d; want 2 (empty-name dropped): %+v", len(byID["d-1"]), byID["d-1"])
	}
	if len(byID["d-2"]) != 1 || byID["d-2"][0].Name != "Google Chrome" {
		t.Errorf("d-2 apps wrong: %+v", byID["d-2"])
	}
	if byID["d-1"][0].Name != "Google Chrome" || byID["d-1"][0].AppID != "app-1" || byID["d-1"][0].Version != "125.0.6422.142" {
		t.Errorf("d-1 first app wrong: %+v", byID["d-1"][0])
	}
	// RawSoftwareItem has no field for sizeInByte/path; managed-device shape has
	// no field for deviceName/userPrincipalName — a leak would be a compile error.
}

// TestClient_ListDetectedApps_WalksNextLink drives the @odata.nextLink cursor
// across TWO pages and asserts the inverted device-centric result is the UNION
// of both pages, including a device whose apps span BOTH pages (the exact bug
// slice 555 left open), and that the loop terminates when nextLink is absent
// (P0-590).
func TestClient_ListDetectedApps_WalksNextLink(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	var pagesServed []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-graph-bearer","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/v1.0/deviceManagement/detectedApps"):
			if r.Method != http.MethodGet {
				t.Errorf("detectedApps method = %s; want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("$skiptoken") == "page2" {
				pagesServed = append(pagesServed, "2")
				// Page 2: app-2 on d-1 (so d-1 spans both pages) + app-3 on d-3.
				// No nextLink -> loop terminates.
				_, _ = w.Write([]byte(`{
				  "value": [
				    {"id":"app-2","displayName":"openssl","version":"3.0.13","managedDevices":[{"id":"d-1"}]},
				    {"id":"app-3","displayName":"Slack","version":"4.0","managedDevices":[{"id":"d-3"}]}
				  ]
				}`))
			} else {
				pagesServed = append(pagesServed, "1")
				// Page 1: app-1 on d-1 + d-2, with an absolute @odata.nextLink.
				next := srv.URL + "/v1.0/deviceManagement/detectedApps?$skiptoken=page2"
				_, _ = w.Write([]byte(`{
				  "@odata.nextLink": "` + next + `",
				  "value": [
				    {"id":"app-1","displayName":"Google Chrome","version":"125","managedDevices":[{"id":"d-1"},{"id":"d-2"}]}
				  ]
				}`))
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(cfgFor(srv))
	got, err := c.ListDetectedApps(context.Background())
	if err != nil {
		t.Fatalf("ListDetectedApps: %v", err)
	}
	if len(pagesServed) != 2 {
		t.Fatalf("served %d pages (%v); want exactly 2 (nextLink walk + terminate)", len(pagesServed), pagesServed)
	}

	byID := map[string][]RawSoftwareItem{}
	for _, d := range got {
		byID[d.DeviceID] = d.Apps
	}
	// d-1 spans both pages: Chrome (page 1) + openssl (page 2) -> the union.
	if len(byID["d-1"]) != 2 {
		t.Errorf("d-1 apps = %d; want 2 (apps span both pages): %+v", len(byID["d-1"]), byID["d-1"])
	}
	if len(byID["d-2"]) != 1 || byID["d-2"][0].Name != "Google Chrome" {
		t.Errorf("d-2 apps wrong: %+v", byID["d-2"])
	}
	// d-3 appears only on page 2 -> would be missed without the walk.
	if len(byID["d-3"]) != 1 || byID["d-3"][0].Name != "Slack" {
		t.Errorf("d-3 (page-2-only device) missing or wrong: %+v", byID["d-3"])
	}
}

func TestClient_ListDetectedApps_TokenError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	cfg := cfgFor(srv)
	cfg.TokenURL = srv.URL + "/tenant-1/oauth2/v2.0/token"
	c := NewClient(cfg)
	if _, err := c.ListDetectedApps(context.Background()); err == nil {
		t.Fatal("want token error")
	}
}

func TestClient_ListDetectedApps_HTTPError(t *testing.T) {
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
	c := NewClient(cfgFor(srv))
	if _, err := c.ListDetectedApps(context.Background()); err == nil {
		t.Fatal("want detectedApps HTTP error")
	}
}
