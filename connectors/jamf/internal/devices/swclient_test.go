package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jamfSoftwareTestServer stands up a fake Jamf Pro that handles the token
// exchange and the computers-inventory software read. NO live Jamf. It is
// page-aware: the supplied fixture is served for page=0, and a terminating empty
// page is served for any later page (mirroring the real Jamf page cursor, which
// stops yielding results past the population). Single-page fixtures therefore
// drive exactly one data page plus one empty terminator.
func jamfSoftwareTestServer(t *testing.T, inventoryJSON string) (*httptest.Server, *requestLog) {
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
			if r.URL.Query().Get("page") == "0" {
				_, _ = w.Write([]byte(inventoryJSON))
			} else {
				_, _ = w.Write([]byte(`{"results":[]}`))
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func TestClient_ListSoftware_DecodesSoftwareFieldsOnly(t *testing.T) {
	t.Parallel()
	// The payload deliberately includes fields the client MUST NOT decode: the
	// executable PATH, a usage stat, and a license key per app.
	inv := `{
	  "results": [
	    {
	      "id": "501",
	      "general": {"name": "ENG-MBP-014"},
	      "applications": [
	        {"name": "Google Chrome", "version": "125.0.6422.142", "bundleId": "com.google.Chrome", "installDate": "2026-01-02",
	         "path": "/Applications/Google Chrome.app", "sizeMegabytes": 540, "licenseKey": "SECRET-XXXX"},
	        {"name": "openssl", "version": "3.0.13"}
	      ]
	    }
	  ]
	}`
	srv, log := jamfSoftwareTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "client-1", "fake-jamf-secret")
	got, err := c.ListSoftware(context.Background())
	if err != nil {
		t.Fatalf("ListSoftware: %v", err)
	}
	if log.authHeader != "Bearer fake-jamf-bearer" {
		t.Errorf("auth header = %q", log.authHeader)
	}
	// The software read MUST request the APPLICATIONS section (that IS the
	// control question for this kind), but never the GPS location or
	// USER_AND_LOCATION owner-contact section.
	if !strings.Contains(log.rawQuery, "APPLICATIONS") {
		t.Errorf("query missing APPLICATIONS section: %q", log.rawQuery)
	}
	for _, banned := range []string{"USER_AND_LOCATION", "LOCATION"} {
		if strings.Contains(log.rawQuery, banned) {
			t.Errorf("query requested %q section (P0-555): %q", banned, log.rawQuery)
		}
	}
	if len(got) != 1 {
		t.Fatalf("device len = %d; want 1", len(got))
	}
	apps := got[0].Apps
	if len(apps) != 2 {
		t.Fatalf("apps len = %d; want 2", len(apps))
	}
	if apps[0].Name != "Google Chrome" || apps[0].Version != "125.0.6422.142" || apps[0].BundleID != "com.google.Chrome" || apps[0].InstallDate != "2026-01-02" {
		t.Errorf("software decode wrong: %+v", apps[0])
	}
	// RawSoftwareItem has no field for path / size / licenseKey, so they could
	// not have been decoded — the struct shape is the guard. Nothing further to
	// assert: a leak would be a compile error.
}

func TestClient_ListSoftware_SkipsMissingComputerID(t *testing.T) {
	t.Parallel()
	inv := `{"results":[{"id":"","applications":[{"name":"X"}]},{"id":"keep","applications":[{"name":"Y"}]}]}`
	srv, _ := jamfSoftwareTestServer(t, inv)
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	got, err := c.ListSoftware(context.Background())
	if err != nil {
		t.Fatalf("ListSoftware: %v", err)
	}
	if len(got) != 1 || got[0].ComputerID != "keep" {
		t.Fatalf("got %+v; want only keep", got)
	}
}

// TestClient_ListSoftware_WalksAllPages drives the page cursor across TWO data
// pages plus the totalCount terminator and asserts the result is the UNION of
// both pages (P0-590). Page 0 and page 1 each carry a distinct computer; the
// loop must stop once gathered >= totalCount.
func TestClient_ListSoftware_WalksAllPages(t *testing.T) {
	t.Parallel()
	page0 := `{"totalCount":2,"results":[{"id":"501","applications":[{"name":"Google Chrome","version":"125"}]}]}`
	page1 := `{"totalCount":2,"results":[{"id":"502","applications":[{"name":"Slack","version":"4.0"}]}]}`

	var pagesServed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-jamf-bearer","expires_in":1200}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/computers-inventory"):
			p := r.URL.Query().Get("page")
			pagesServed = append(pagesServed, p)
			w.Header().Set("Content-Type", "application/json")
			switch p {
			case "0":
				_, _ = w.Write([]byte(page0))
			case "1":
				_, _ = w.Write([]byte(page1))
			default:
				t.Errorf("loop did not terminate: requested page %q past totalCount", p)
				_, _ = w.Write([]byte(`{"totalCount":2,"results":[]}`))
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	got, err := c.ListSoftware(context.Background())
	if err != nil {
		t.Fatalf("ListSoftware: %v", err)
	}
	// Both pages must be emitted (union), and the loop must terminate after
	// exactly the two data pages.
	if len(got) != 2 {
		t.Fatalf("device count = %d; want 2 (union of both pages): %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ComputerID] = true
	}
	if !ids["501"] || !ids["502"] {
		t.Errorf("missing a page's device: got ids %v; want 501 + 502", ids)
	}
	if len(pagesServed) != 2 {
		t.Errorf("served %d pages (%v); want exactly 2 (loop must terminate on totalCount)", len(pagesServed), pagesServed)
	}
}

func TestClient_ListSoftware_TokenError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "c", "fake-secret")
	if _, err := c.ListSoftware(context.Background()); err == nil {
		t.Fatal("want token error")
	}
}

func TestClient_ListSoftware_HTTPError(t *testing.T) {
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
	if _, err := c.ListSoftware(context.Background()); err == nil {
		t.Fatal("want inventory HTTP error")
	}
}
