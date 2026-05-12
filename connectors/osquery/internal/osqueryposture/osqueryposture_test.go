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

// fakeFleet wires httptest server responses for ListHosts + GetHost.
func fakeFleet(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/fleet/hosts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"hosts": [
				{"id": 1, "uuid": "uuid-A", "hostname": "mac-1", "platform": "darwin", "os_version": "macOS 15.1"},
				{"id": 2, "uuid": "uuid-B", "hostname": "win-1", "platform": "windows", "os_version": "Windows 11"}
			]
		}`))
	})
	mux.HandleFunc("/api/v1/fleet/hosts/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"host": {
				"id": 1, "uuid": "uuid-A", "hostname": "mac-1",
				"platform": "darwin", "os_version": "macOS 15.1",
				"disk_encryption_enabled": true,
				"screenlock_enabled": true,
				"firewall_enabled": true,
				"mdm_enrolled": true
			}
		}`))
	})
	mux.HandleFunc("/api/v1/fleet/hosts/2", func(w http.ResponseWriter, _ *http.Request) {
		// Same shape; deliberately disk-encryption off so evaluate() flips
		// to FAIL on this host.
		_, _ = w.Write([]byte(`{
			"host": {
				"id": 2, "uuid": "uuid-B", "hostname": "win-1",
				"platform": "windows", "os_version": "Windows 11",
				"disk_encryption_enabled": false,
				"screenlock_enabled": true,
				"firewall_enabled": false,
				"mdm_enrolled": false
			}
		}`))
	})
	return httptest.NewServer(mux)
}

func TestPullFromFleet_TwoHosts_OnePassOneFail(t *testing.T) {
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
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	byUUID := map[string]osqueryposture.HostPosture{}
	for _, r := range rows {
		byUUID[r.HostUUID] = r
	}
	if a := byUUID["uuid-A"]; a.Result != osqueryposture.ResultPass {
		t.Errorf("uuid-A result = %q; want pass (%+v)", a.Result, a)
	}
	if a := byUUID["uuid-A"]; a.Platform != "darwin" {
		t.Errorf("uuid-A platform = %q", a.Platform)
	}
	if a := byUUID["uuid-A"]; !a.DiskEncryptionEnabled || !a.ScreenLockEnabled {
		t.Errorf("uuid-A booleans wrong: %+v", a)
	}
	if b := byUUID["uuid-B"]; b.Result != osqueryposture.ResultFail {
		t.Errorf("uuid-B result = %q; want fail (%+v)", b.Result, b)
	}
	if b := byUUID["uuid-B"]; b.MDMEnrolled {
		t.Errorf("uuid-B mdm_enrolled = true; want false")
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
			2: {ID: 2, UUID: "uuid-real", Hostname: "real", Platform: "linux",
				DiskEncryptionEnabled: true, ScreenLockEnabled: true},
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
			{ID: 1, UUID: "uuid-X", Hostname: "x", Platform: "darwin"},
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
	if rows[0].Result != osqueryposture.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", rows[0].Result)
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
	if r.HostUUID != "local-uuid" || r.Platform != "darwin" || !r.DiskEncryptionEnabled || !r.ScreenLockEnabled {
		t.Errorf("local row wrong: %+v", r)
	}
	if r.MDMEnrolled {
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
