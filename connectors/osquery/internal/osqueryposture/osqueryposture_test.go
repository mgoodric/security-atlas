package osqueryposture_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryauth"
	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryposture"
)

// The Fleet fixtures below are trimmed from the example responses in
// Fleet's REST API docs (fleetdm.com/docs/rest-api/rest-api, "List hosts"
// and "Get host", read 2026-11-21): the keys, envelope and example values
// are Fleet's, and only fields the connector reads plus a few neighbours
// are kept. Host 2 is the docs' shape with disk encryption off; host 3
// omits disk_encryption_enabled, which the docs say Fleet does when it
// doesn't know the status (#2912).
const (
	fleetListHosts = `{
		"hosts": [
			{"created_at": "2020-11-05T05:09:44Z", "id": 1, "seen_time": "2020-11-05T06:03:39Z",
			 "hostname": "Annas-MacBook-Pro.local", "uuid": "392547dc-0000-0000-a87a-d701ff75bc65",
			 "platform": "darwin", "os_version": "macOS 15.2", "computer_name": "Anna's MacBook Pro",
			 "disk_encryption_enabled": true, "status": "offline",
			 "mdm": {"encryption_key_available": false, "enrollment_status": "Pending", "name": "Fleet"}},
			{"id": 2, "seen_time": "2021-08-19T21:14:58Z", "hostname": "win-1",
			 "uuid": "uuid-B", "platform": "windows", "os_version": "Windows 11"},
			{"id": 3, "seen_time": "2021-08-19T21:14:58Z", "hostname": "linux-1",
			 "uuid": "uuid-C", "platform": "ubuntu", "os_version": "Ubuntu 22.04"}
		]
	}`
	fleetGetHost1 = `{
		"host": {
			"created_at": "2021-08-19T02:02:22Z", "id": 1, "seen_time": "2021-08-19T21:14:58Z",
			"hostname": "Annas-MacBook-Pro.local", "uuid": "392547dc-0000-0000-a87a-d701ff75bc65",
			"platform": "darwin", "os_version": "macOS 15.2", "computer_name": "Anna's MacBook Pro",
			"disk_encryption_enabled": true, "status": "online",
			"mdm": {"encryption_key_available": true, "enrollment_status": "On (manual)",
			        "is_personal_enrollment": false, "name": "Fleet", "connected_to_fleet": true,
			        "device_status": "unlocked", "pending_action": ""},
			"policies": [{"id": 1, "name": "SomeQuery", "query": "SELECT * FROM foo;",
			              "platform": "windows,linux", "response": "pass", "critical": false}]
		}
	}`
	fleetGetHost2 = `{
		"host": {
			"id": 2, "seen_time": "2021-08-19T21:14:58Z", "hostname": "win-1", "uuid": "uuid-B",
			"platform": "windows", "os_version": "Windows 11",
			"disk_encryption_enabled": false, "status": "online",
			"mdm": {"enrollment_status": "On (automatic)", "name": "Fleet"}
		}
	}`
	fleetGetHost3 = `{
		"host": {
			"id": 3, "seen_time": "2021-08-19T21:14:58Z", "hostname": "linux-1", "uuid": "uuid-C",
			"platform": "ubuntu", "os_version": "Ubuntu 22.04", "status": "offline"
		}
	}`
)

// fakeFleet wires httptest server responses for ListHosts + GetHost.
func fakeFleet(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range map[string]string{
		"/api/v1/fleet/hosts":   fleetListHosts,
		"/api/v1/fleet/hosts/1": fleetGetHost1,
		"/api/v1/fleet/hosts/2": fleetGetHost2,
		"/api/v1/fleet/hosts/3": fleetGetHost3,
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		})
	}
	return httptest.NewServer(mux)
}

func pullFake(t *testing.T) map[string]osqueryposture.HostPosture {
	t.Helper()
	srv := fakeFleet(t)
	t.Cleanup(srv.Close)
	creds, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "fleet-test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	rows, err := osqueryposture.PullFromFleet(context.Background(), client, func() time.Time {
		return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("PullFromFleet: %v", err)
	}
	byUUID := map[string]osqueryposture.HostPosture{}
	for _, r := range rows {
		byUUID[r.HostUUID] = r
	}
	return byUUID
}

func boolIs(b *bool, want bool) bool { return b != nil && *b == want }

// Fleet's Get host has no firewall or screen-lock field, so a Fleet host
// never passes: disk encryption on is inconclusive (screen lock unread),
// off is fail, and an omitted disk_encryption_enabled stays unread (#2912).
func TestPullFromFleet_DocumentedShapes(t *testing.T) {
	byUUID := pullFake(t)
	if len(byUUID) != 3 {
		t.Fatalf("rows = %d; want 3", len(byUUID))
	}
	a := byUUID["392547dc-0000-0000-a87a-d701ff75bc65"]
	if a.Result != osqueryposture.ResultInconclusive || a.Reason != "screen_lock not reported" {
		t.Errorf("mac result = %q (%q); want inconclusive, screen_lock not reported", a.Result, a.Reason)
	}
	if a.Platform != "darwin" || a.Hostname != "Annas-MacBook-Pro.local" {
		t.Errorf("mac identity wrong: %+v", a)
	}
	if !boolIs(a.DiskEncryptionEnabled, true) || !boolIs(a.MDMEnrolled, true) {
		t.Errorf("mac disk_encryption / mdm wrong: %+v", a)
	}
	if a.ScreenLockEnabled != nil || a.FirewallEnabled != nil {
		t.Errorf("mac screen lock / firewall read from a response that has neither: %+v", a)
	}
	if want := time.Date(2021, 8, 19, 21, 14, 58, 0, time.UTC); !a.LastSeen.Equal(want) {
		t.Errorf("mac LastSeen = %v; want detail seen_time %v", a.LastSeen, want)
	}
	if want := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC); !a.ObservedAt.Equal(want) {
		t.Errorf("mac ObservedAt = %v; want pull time %v", a.ObservedAt, want)
	}

	b := byUUID["uuid-B"]
	if b.Result != osqueryposture.ResultFail || b.Reason != "disk_encryption_disabled" {
		t.Errorf("win result = %q (%q); want fail", b.Result, b.Reason)
	}
	if !boolIs(b.DiskEncryptionEnabled, false) || !boolIs(b.MDMEnrolled, true) {
		t.Errorf("win booleans wrong: %+v", b)
	}

	c := byUUID["uuid-C"]
	if c.Result != osqueryposture.ResultInconclusive {
		t.Errorf("linux result = %q; want inconclusive", c.Result)
	}
	if c.DiskEncryptionEnabled != nil || c.MDMEnrolled != nil {
		t.Errorf("linux: omitted keys read as values: %+v", c)
	}
	if c.Platform != "linux" {
		t.Errorf("linux platform = %q", c.Platform)
	}
}

func TestPullFromFleet_EvaluateLadder(t *testing.T) {
	tr, fa := true, false
	cases := []struct {
		name       string
		disk, lock *bool
		want       osqueryposture.Result
	}{
		{"both_on", &tr, &tr, osqueryposture.ResultPass},
		{"disk_off", &fa, nil, osqueryposture.ResultFail},
		{"lock_off", &tr, &fa, osqueryposture.ResultFail},
		{"lock_unread", &tr, nil, osqueryposture.ResultInconclusive},
		{"disk_unread", nil, &tr, osqueryposture.ResultInconclusive},
		{"nothing_read", nil, nil, osqueryposture.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeLocal{rows: []map[string]string{{"uuid": "u", "platform": "darwin",
				"disk_encryption_enabled": strBool(tc.disk), "screen_lock_enabled": strBool(tc.lock)}}}
			rows, err := osqueryposture.PullFromLocal(context.Background(), q, nil)
			if err != nil || len(rows) != 1 {
				t.Fatalf("PullFromLocal: %v (%d rows)", err, len(rows))
			}
			if rows[0].Result != tc.want {
				t.Errorf("result = %q (%q); want %q", rows[0].Result, rows[0].Reason, tc.want)
			}
		})
	}
}

func strBool(b *bool) string {
	switch {
	case b == nil:
		return ""
	case *b:
		return "1"
	default:
		return "0"
	}
}

func TestPullFromFleet_SkipsHostWithEmptyUUID(t *testing.T) {
	// fakeAPI is a direct FleetAPI implementation — no HTTP layer. The
	// test pins the anti-criterion: empty host_uuid MUST be skipped,
	// never emitted with a fabricated key.
	api := &fakeAPI{
		hosts: []osqueryposture.HostListEntry{
			{ID: 1, UUID: "", Hostname: "ghost"},
			{ID: 2, UUID: "uuid-real", Hostname: "real", Platform: "linux"},
		},
		detail: map[uint64]osqueryposture.HostDetail{
			2: {ID: 2, UUID: "uuid-real", Hostname: "real", Platform: "linux"},
		},
	}
	rows, err := osqueryposture.PullFromFleet(context.Background(), api, nil)
	if err != nil {
		t.Fatalf("PullFromFleet: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d; want 1 (empty-uuid host skipped)", len(rows))
	}
	if rows[0].HostUUID != "uuid-real" {
		t.Fatalf("survivor uuid = %q", rows[0].HostUUID)
	}
}

func TestPullFromFleet_DetailFailureIsInconclusive(t *testing.T) {
	api := &fakeAPI{
		hosts: []osqueryposture.HostListEntry{
			{ID: 1, UUID: "uuid-X", Hostname: "x", Platform: "darwin", SeenTime: "2021-08-19T21:14:58Z"},
		},
		err: errors.New("simulated 503"),
	}
	rows, err := osqueryposture.PullFromFleet(context.Background(), api, nil)
	if err != nil {
		t.Fatalf("PullFromFleet: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.Result != osqueryposture.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", r.Result)
	}
	// #2912: nothing was read, so nothing may read as false.
	if r.DiskEncryptionEnabled != nil || r.ScreenLockEnabled != nil || r.FirewallEnabled != nil || r.MDMEnrolled != nil {
		t.Errorf("detail fetch failed but posture fields set: %+v", r)
	}
	if want := time.Date(2021, 8, 19, 21, 14, 58, 0, time.UTC); !r.LastSeen.Equal(want) {
		t.Errorf("LastSeen = %v; want the list's seen_time %v", r.LastSeen, want)
	}
}

func TestPullFromLocal_OneRow(t *testing.T) {
	q := &fakeLocal{rows: []map[string]string{
		{
			"uuid":                    "local-uuid",
			"hostname":                "dev-laptop",
			"platform":                "darwin",
			"os_version":              "15.2",
			"disk_encryption_enabled": "1",
			"screen_lock_enabled":     "true",
			"firewall_enabled":        "1",
			"mdm_enrolled":            "0",
		},
	}}
	rows, err := osqueryposture.PullFromLocal(context.Background(), q, func() time.Time {
		return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("PullFromLocal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.HostUUID != "local-uuid" || r.Platform != "darwin" || !boolIs(r.DiskEncryptionEnabled, true) || !boolIs(r.ScreenLockEnabled, true) {
		t.Errorf("local row wrong: %+v", r)
	}
	if !boolIs(r.MDMEnrolled, false) {
		t.Errorf("mdm_enrolled = true; want false ('0' → false)")
	}
	if r.Result != osqueryposture.ResultPass {
		t.Errorf("result = %q; want pass", r.Result)
	}
}

func TestPullFromLocal_SkipsEmptyUUID(t *testing.T) {
	q := &fakeLocal{rows: []map[string]string{{"uuid": ""}}}
	rows, err := osqueryposture.PullFromLocal(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("PullFromLocal: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d; want 0 (empty uuid skipped)", len(rows))
	}
}

func TestFleetClient_RateLimitSurfacesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	creds, _ := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "x"})
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	_, err := client.ListHosts(context.Background())
	if err == nil {
		t.Fatal("expected error on 429")
	}
	var apiErr *osqueryposture.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError; got %T", err)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d; want 429", apiErr.Status)
	}
	if apiErr.Retry != "60" {
		t.Errorf("Retry = %q; want 60", apiErr.Retry)
	}
}

func TestFleetClient_GetHost404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	creds, _ := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "x"})
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	_, err := client.GetHost(context.Background(), 42)
	if !errors.Is(err, osqueryposture.ErrHostNotFound) {
		t.Fatalf("err = %v; want ErrHostNotFound", err)
	}
}

func TestFleetClient_AuthorizationHeaderSent(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"hosts": []}`))
	}))
	t.Cleanup(srv.Close)
	creds, _ := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "tok-abc"})
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	if _, err := client.ListHosts(context.Background()); err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if !strings.HasPrefix(captured, "Bearer ") {
		t.Fatalf("Authorization = %q; want Bearer prefix", captured)
	}
	if captured != "Bearer tok-abc" {
		t.Fatalf("Authorization = %q", captured)
	}
}

// ---- Fakes ----

type fakeAPI struct {
	hosts  []osqueryposture.HostListEntry
	detail map[uint64]osqueryposture.HostDetail
	err    error
}

func (f *fakeAPI) ListHosts(_ context.Context) ([]osqueryposture.HostListEntry, error) {
	return f.hosts, nil
}

func (f *fakeAPI) GetHost(_ context.Context, id uint64) (*osqueryposture.HostDetail, error) {
	if f.err != nil {
		return nil, f.err
	}
	d, ok := f.detail[id]
	if !ok {
		return nil, errors.New("fakeAPI: no detail for id")
	}
	return &d, nil
}

type fakeLocal struct {
	rows []map[string]string
}

func (f *fakeLocal) Query(_ context.Context, _ string) ([]map[string]string, error) {
	return f.rows, nil
}
