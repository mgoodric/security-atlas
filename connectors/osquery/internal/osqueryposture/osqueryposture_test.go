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
// doesn't know the status (#2912). Host 1's policies[] is the docs' Get
// host example array verbatim (ids 2, 3, 1: "fail", "", "pass"); Fleet's
// own example names are kept so the shape stays traceable (#2921). Host 1's
// disk keys (174.98 / 71 / 246) and its labels[] entry are the docs' Get
// host macOS example values; host 2 reports a zero total, the shape of a
// host with no disk reading yet (#2826).
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
			"gigs_disk_space_available": 174.98, "percent_disk_space_available": 71,
			"gigs_total_disk_space": 246,
			"labels": [
				{"created_at": "2021-08-19T02:02:17Z", "updated_at": "2021-08-19T02:02:17Z",
				 "id": 12, "name": "Hosts with Chrome installed", "description": "",
				 "query": "SELECT * FROM apps WHERE name LIKE \"%Chrome%\"", "platform": "",
				 "label_type": "regular", "label_membership_type": "dynamic"}
			],
			"mdm": {"encryption_key_available": true, "enrollment_status": "On (manual)",
			        "is_personal_enrollment": false, "name": "Fleet", "connected_to_fleet": true,
			        "device_status": "unlocked", "pending_action": ""},
			"policies": [
				{"id": 2, "name": "SomeQuery2", "query": "SELECT * FROM bar;",
				 "description": "this is another query", "resolution": "fix with these other steps...",
				 "platform": "darwin", "response": "fail", "critical": false},
				{"id": 3, "name": "SomeQuery3", "query": "SELECT * FROM baz;",
				 "description": "", "resolution": "", "platform": "", "response": "", "critical": false},
				{"id": 1, "name": "SomeQuery", "query": "SELECT * FROM foo;",
				 "description": "this is a query", "resolution": "fix with these steps...",
				 "platform": "windows,linux", "response": "pass", "critical": false}
			]
		}
	}`
	fleetGetHost2 = `{
		"host": {
			"id": 2, "seen_time": "2021-08-19T21:14:58Z", "hostname": "win-1", "uuid": "uuid-B",
			"platform": "windows", "os_version": "Windows 11",
			"disk_encryption_enabled": false, "status": "online",
			"gigs_disk_space_available": 0, "percent_disk_space_available": 0,
			"gigs_total_disk_space": 0,
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

// Host report fixtures (#2936) follow Fleet's "List host's reports" and
// "Get host's report data" example responses (fleetdm.com/docs/rest-api/
// rest-api, read 2026-11-22): the envelope and keys are Fleet's, and the
// first list entry is the docs' "List USB devices" example. The
// "AV definitions" report (id 60), its "last_updated" column and the epoch
// value are ours: Fleet documents no anti-malware report, and the query
// that would produce the column was never run against a live host. Host 2
// has the report but no stored results (the docs' empty-results shape);
// host 3 has no reports route, so its lookup fails with a 404.
const (
	fleetHost1Reports = `{
		"reports": [
			{"id": 31, "name": "List USB devices", "description": "Retrieves USB devices and their details.",
			 "query": "SELECT model, vendor FROM usb_devices;", "logging": "snapshot", "discard_data": false,
			 "total_results_count": 4, "last_fetched": "2021-01-19T17:08:31Z",
			 "first_result_columns": {"model": "USB 2.0 Hub", "vendor": "VIA Labs, Inc."}},
			{"id": 60, "name": "AV definitions", "description": "", "query": "SELECT ...;",
			 "logging": "snapshot", "discard_data": false, "total_results_count": 1,
			 "last_fetched": "2021-08-19T21:00:00Z", "first_result_columns": {"last_updated": "1629334800"}}
		],
		"meta": {"has_next_results": false, "has_previous_results": false},
		"count": 2
	}`
	fleetHost1Report60 = `{
		"query_id": 60, "report_id": 60, "host_id": 1, "host_name": "Annas-MacBook-Pro.local",
		"last_fetched": "2021-08-19T21:00:00Z", "report_clipped": false,
		"results": [{"columns": {"last_updated": "1629334800"}}]
	}`
	fleetHost2Reports = `{
		"reports": [
			{"id": 60, "name": "AV definitions", "logging": "snapshot", "discard_data": false,
			 "total_results_count": 0, "last_fetched": "2021-08-19T21:00:00Z", "first_result_columns": null}
		],
		"meta": {"has_next_results": false, "has_previous_results": false},
		"count": 1
	}`
	fleetHost2Report60 = `{
		"query_id": 60, "report_id": 60, "host_id": 2, "host_name": "win-1",
		"last_fetched": "2021-08-19T21:00:00Z", "report_clipped": false, "results": []
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

		"/api/v1/fleet/hosts/1/reports":    fleetHost1Reports,
		"/api/v1/fleet/hosts/1/reports/60": fleetHost1Report60,
		"/api/v1/fleet/hosts/2/reports":    fleetHost2Reports,
		"/api/v1/fleet/hosts/2/reports/60": fleetHost2Report60,
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		})
	}
	return httptest.NewServer(mux)
}

func pullFake(t *testing.T) map[string]osqueryposture.HostPosture {
	t.Helper()
	return pullFakeWith(t, osqueryposture.FleetPolicies{})
}

func pullFakeWith(t *testing.T, pol osqueryposture.FleetPolicies) map[string]osqueryposture.HostPosture {
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
	}, pol)
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

// #2921: with policies named, firewall and screen lock come from the host's
// policies[] response through the real HTTP decode. Host 1 passes
// "SomeQuery" and fails "SomeQuery2"; hosts 2 and 3 carry no policies[],
// so both fields stay unread there.
func TestPullFromFleet_NamedPoliciesFromDocumentedShape(t *testing.T) {
	byUUID := pullFakeWith(t, osqueryposture.FleetPolicies{Firewall: "SomeQuery", ScreenLock: "SomeQuery2"})
	a := byUUID["392547dc-0000-0000-a87a-d701ff75bc65"]
	if !boolIs(a.FirewallEnabled, true) || a.FirewallSource != "fleet_policy:SomeQuery" {
		t.Errorf("mac firewall = %v (%q); want true from fleet_policy:SomeQuery", a.FirewallEnabled, a.FirewallSource)
	}
	if !boolIs(a.ScreenLockEnabled, false) || a.ScreenLockSource != "fleet_policy:SomeQuery2" {
		t.Errorf("mac screen lock = %v (%q); want false from fleet_policy:SomeQuery2", a.ScreenLockEnabled, a.ScreenLockSource)
	}
	if a.Result != osqueryposture.ResultFail || a.Reason != "screen_lock_disabled" {
		t.Errorf("mac result = %q (%q); want fail, screen_lock_disabled", a.Result, a.Reason)
	}
	for _, u := range []string{"uuid-B", "uuid-C"} {
		h := byUUID[u]
		if h.FirewallEnabled != nil || h.ScreenLockEnabled != nil || h.FirewallSource != "" || h.ScreenLockSource != "" {
			t.Errorf("%s has no policies[] but firewall/screen lock read: %+v", u, h)
		}
	}

	// Both on: the documented "pass" policy for both questions lets a
	// Fleet host pass, which it never could before #2921.
	pass := pullFakeWith(t, osqueryposture.FleetPolicies{Firewall: "SomeQuery", ScreenLock: "SomeQuery"})["392547dc-0000-0000-a87a-d701ff75bc65"]
	if pass.Result != osqueryposture.ResultPass {
		t.Errorf("mac with both policies passing = %q (%q); want pass", pass.Result, pass.Reason)
	}

	// The documented empty response is unread, not false.
	empty := pullFakeWith(t, osqueryposture.FleetPolicies{Firewall: "SomeQuery3", ScreenLock: "SomeQuery3"})["392547dc-0000-0000-a87a-d701ff75bc65"]
	if empty.FirewallEnabled != nil || empty.ScreenLockEnabled != nil || empty.FirewallSource != "" {
		t.Errorf("empty policy response read as a value: %+v", empty)
	}
	if empty.Result != osqueryposture.ResultInconclusive || empty.Reason != "screen_lock not reported" {
		t.Errorf("mac with empty responses = %q (%q); want inconclusive", empty.Result, empty.Reason)
	}
}

// #2826: disk use from the documented disk keys, host role from a named
// label, antimalware and EDR from named policies, all through the real HTTP
// decode. Host 1 carries the label, passes "SomeQuery" and fails
// "SomeQuery2"; host 2 reports a zero disk total; host 3 has none of it.
func TestPullFromFleet_CapacityRoleAntimalwareEDR(t *testing.T) {
	byUUID := pullFakeWith(t, osqueryposture.FleetPolicies{
		Antimalware:     "SomeQuery",
		EDR:             "SomeQuery2",
		ProductionLabel: "Hosts with Chrome installed",
	})
	mac := byUUID["392547dc-0000-0000-a87a-d701ff75bc65"]
	if mac.DiskUsedPercent == nil || *mac.DiskUsedPercent != 29 {
		t.Errorf("mac disk_used_percent = %v; want 29 (100 - 71)", mac.DiskUsedPercent)
	}
	if mac.HostRole != "production" || mac.HostRoleSource != "fleet_label:Hosts with Chrome installed" {
		t.Errorf("mac role = %q (%q); want production from the label", mac.HostRole, mac.HostRoleSource)
	}
	if !boolIs(mac.AntimalwareProtected, true) || mac.AntimalwareSource != "fleet_policy:SomeQuery" {
		t.Errorf("mac antimalware = %v (%q); want true from SomeQuery", strBool(mac.AntimalwareProtected), mac.AntimalwareSource)
	}
	if !boolIs(mac.EDRHealthy, false) || mac.EDRSource != "fleet_policy:SomeQuery2" {
		t.Errorf("mac edr = %v (%q); want false from SomeQuery2", strBool(mac.EDRHealthy), mac.EDRSource)
	}
	// The connector's own verdict is unchanged: disk encryption + screen lock.
	if mac.Result != osqueryposture.ResultInconclusive {
		t.Errorf("mac result = %q; want inconclusive (screen lock unread)", mac.Result)
	}
	for _, u := range []string{"uuid-B", "uuid-C"} {
		h := byUUID[u]
		if h.DiskUsedPercent != nil {
			t.Errorf("%s disk_used_percent = %v; want unread", u, *h.DiskUsedPercent)
		}
		if h.HostRole != "" || h.AntimalwareProtected != nil || h.EDRHealthy != nil {
			t.Errorf("%s has no labels or policies but role/antimalware/edr read: %+v", u, h)
		}
	}
}

// Without the names, none of role, antimalware or EDR is read; disk use
// needs no name.
func TestPullFromFleet_NewFieldsUnreadWithoutNames(t *testing.T) {
	mac := pullFake(t)["392547dc-0000-0000-a87a-d701ff75bc65"]
	if mac.HostRole != "" || mac.AntimalwareProtected != nil || mac.EDRHealthy != nil {
		t.Errorf("unnamed label/policies read: %+v", mac)
	}
	if mac.DiskUsedPercent == nil || *mac.DiskUsedPercent != 29 {
		t.Errorf("disk_used_percent = %v; want 29", mac.DiskUsedPercent)
	}
}

// diskUsedPercent rules: a missing percentage or total, a zero total, or a
// percentage outside 0-100 is unread.
func TestPullFromFleet_DiskUsedPercentRules(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name         string
		avail, total *float64
		want         *float64
	}{
		{"documented", f(71), f(246), f(29)},
		{"full_disk", f(0), f(246), f(100)},
		{"empty_disk", f(100), f(246), f(0)},
		{"no_percent", nil, f(246), nil},
		{"no_total", f(71), nil, nil},
		{"zero_total", f(0), f(0), nil},
		{"over_100", f(120), f(246), nil},
		{"negative", f(-1), f(246), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{
				hosts:  []osqueryposture.HostListEntry{{ID: 1, UUID: "u", Hostname: "h"}},
				detail: map[uint64]osqueryposture.HostDetail{1: {ID: 1, UUID: "u", PercentDiskSpaceAvailable: tc.avail, GigsTotalDiskSpace: tc.total}},
			}
			rows, err := osqueryposture.PullFromFleet(context.Background(), api, nil, osqueryposture.FleetPolicies{})
			if err != nil {
				t.Fatalf("PullFromFleet: %v", err)
			}
			got := rows[0].DiskUsedPercent
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("got %v; want unread", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Errorf("got %v; want %v", got, *tc.want)
			}
		})
	}
}

func TestPullFromFleet_PolicyResponseRules(t *testing.T) {
	tr, fa := true, false
	pl := func(name, resp string) osqueryposture.HostPolicy {
		return osqueryposture.HostPolicy{Name: name, Response: resp}
	}
	cases := []struct {
		name     string
		policy   string
		policies []osqueryposture.HostPolicy
		want     *bool
	}{
		{"pass", "fw", []osqueryposture.HostPolicy{pl("fw", "pass")}, &tr},
		{"fail", "fw", []osqueryposture.HostPolicy{pl("fw", "fail")}, &fa},
		{"empty_response", "fw", []osqueryposture.HostPolicy{pl("fw", "")}, nil},
		{"unknown_response", "fw", []osqueryposture.HostPolicy{pl("fw", "error")}, nil},
		{"missing_policy", "fw", []osqueryposture.HostPolicy{pl("other", "pass")}, nil},
		{"no_policies", "fw", nil, nil},
		{"not_configured", "", []osqueryposture.HostPolicy{pl("", "pass"), pl("fw", "pass")}, nil},
		{"name_is_exact", "fw", []osqueryposture.HostPolicy{pl("FW", "pass"), pl("fw ", "pass")}, nil},
		{"duplicates_agree", "fw", []osqueryposture.HostPolicy{pl("fw", "fail"), pl("fw", "fail")}, &fa},
		{"duplicates_disagree", "fw", []osqueryposture.HostPolicy{pl("fw", "pass"), pl("fw", "fail")}, nil},
		{"duplicate_unreported", "fw", []osqueryposture.HostPolicy{pl("fw", "pass"), pl("fw", "")}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{
				hosts:  []osqueryposture.HostListEntry{{ID: 1, UUID: "u", Platform: "darwin"}},
				detail: map[uint64]osqueryposture.HostDetail{1: {ID: 1, UUID: "u", Policies: tc.policies}},
			}
			rows, err := osqueryposture.PullFromFleet(context.Background(), api, nil,
				osqueryposture.FleetPolicies{Firewall: tc.policy, ScreenLock: tc.policy})
			if err != nil || len(rows) != 1 {
				t.Fatalf("PullFromFleet: %v (%d rows)", err, len(rows))
			}
			r := rows[0]
			for field, got := range map[string]*bool{"firewall": r.FirewallEnabled, "screen_lock": r.ScreenLockEnabled} {
				switch {
				case tc.want == nil && got != nil:
					t.Errorf("%s = %v; want unread", field, *got)
				case tc.want != nil && (got == nil || *got != *tc.want):
					t.Errorf("%s = %v; want %v", field, got, *tc.want)
				}
			}
			wantSrc := ""
			if tc.want != nil {
				wantSrc = "fleet_policy:" + tc.policy
			}
			if r.FirewallSource != wantSrc || r.ScreenLockSource != wantSrc {
				t.Errorf("sources = %q / %q; want %q", r.FirewallSource, r.ScreenLockSource, wantSrc)
			}
		})
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
	rows, err := osqueryposture.PullFromFleet(context.Background(), api, nil, osqueryposture.FleetPolicies{})
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
	rows, err := osqueryposture.PullFromFleet(context.Background(), api, nil, osqueryposture.FleetPolicies{})
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
			"disk_used_percent":       "42.5",
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
	if r.DiskUsedPercent == nil || *r.DiskUsedPercent != 42.5 {
		t.Errorf("disk_used_percent = %v; want 42.5", r.DiskUsedPercent)
	}
}

// #2826: a NULL, non-numeric or out-of-range disk_used_percent column is
// unread, never 0.
func TestPullFromLocal_DiskUsedPercentUnread(t *testing.T) {
	for _, v := range []string{"", "n/a", "-0.1", "100.1"} {
		q := &fakeLocal{rows: []map[string]string{{"uuid": "u", "disk_used_percent": v}}}
		rows, err := osqueryposture.PullFromLocal(context.Background(), q, nil)
		if err != nil || len(rows) != 1 {
			t.Fatalf("PullFromLocal: %v", err)
		}
		if rows[0].DiskUsedPercent != nil {
			t.Errorf("disk_used_percent %q read as %v; want unread", v, *rows[0].DiskUsedPercent)
		}
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

// #2936: the definitions date is read from the named report's column, over
// Fleet's documented host-report endpoints. Host 1 has one row (epoch
// seconds); host 2 has the report but no results, so it is unread; host 3's
// lookup fails, so it is unread and the reason names the failure.
func TestPullFromFleet_DefinitionsReport(t *testing.T) {
	rows := pullFakeWith(t, osqueryposture.FleetPolicies{
		DefinitionsReport: "AV definitions",
		DefinitionsColumn: "last_updated",
	})
	h1 := rows["392547dc-0000-0000-a87a-d701ff75bc65"]
	if want := time.Unix(1629334800, 0).UTC(); !h1.AntimalwareDefinitionsUpdatedAt.Equal(want) {
		t.Errorf("host 1 definitions = %v; want %v", h1.AntimalwareDefinitionsUpdatedAt, want)
	}
	if h1.AntimalwareDefinitionsSource != "fleet_report:AV definitions" {
		t.Errorf("host 1 source = %q", h1.AntimalwareDefinitionsSource)
	}
	h2 := rows["uuid-B"]
	if !h2.AntimalwareDefinitionsUpdatedAt.IsZero() || h2.AntimalwareDefinitionsSource != "" {
		t.Errorf("host 2 has no results; got %v %q", h2.AntimalwareDefinitionsUpdatedAt, h2.AntimalwareDefinitionsSource)
	}
	h3 := rows["uuid-C"]
	if !h3.AntimalwareDefinitionsUpdatedAt.IsZero() || h3.AntimalwareDefinitionsSource != "" {
		t.Errorf("host 3 lookup failed; got %v %q", h3.AntimalwareDefinitionsUpdatedAt, h3.AntimalwareDefinitionsSource)
	}
	if !strings.Contains(h3.Reason, "antimalware definitions report: fleet: HTTP 404") {
		t.Errorf("host 3 reason = %q; want the report failure named", h3.Reason)
	}
}

// #2936: the report name goes to Fleet as the `query` search keywords.
func TestFleetClient_ListHostReportsSendsName(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"reports": [], "count": 0}`))
	}))
	t.Cleanup(srv.Close)
	creds, _ := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "x"})
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	if _, err := client.ListHostReports(context.Background(), 7, "AV definitions & more"); err != nil {
		t.Fatalf("ListHostReports: %v", err)
	}
	if gotPath != "/api/v1/fleet/hosts/7/reports" || gotQuery != "AV definitions & more" {
		t.Errorf("path = %q query = %q", gotPath, gotQuery)
	}
}

// #2936: the rules for turning report rows into one date.
func TestPullFromFleet_DefinitionsReportRows(t *testing.T) {
	const col = "last_updated"
	named := []osqueryposture.HostReportSummary{{ID: 9, Name: "AV"}}
	row := func(v any) osqueryposture.HostReportRow {
		return osqueryposture.HostReportRow{Columns: map[string]any{col: v}}
	}
	rows := func(vs ...any) []osqueryposture.HostReportRow {
		out := make([]osqueryposture.HostReportRow, 0, len(vs))
		for _, v := range vs {
			out = append(out, row(v))
		}
		return out
	}
	// pull is the connector's clock for these cases; "now + 1 h" is
	// 2026-11-22T13:00:00Z.
	pull := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		reports []osqueryposture.HostReportSummary
		rows    []osqueryposture.HostReportRow
		want    time.Time
		reason  string // substring of the record's reason; "" means no report note
	}{
		{"epoch_seconds", named, rows("1763607600"), time.Unix(1763607600, 0).UTC(), ""},
		{"rfc3339_with_offset", named, rows("2026-11-20T03:00:00+01:00"), time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC), ""},
		{"oldest_of_two_rows", named, rows("1763607600", "1763000000"), time.Unix(1763000000, 0).UTC(), ""},
		{"oldest_readable_despite_a_bad_row", named, rows("1763607600", "yesterday", "1763000000"), time.Unix(1763000000, 0).UTC(), "1 of 3 rows unread (row 2 not RFC 3339 or epoch seconds)"},
		{"missing_column", named, []osqueryposture.HostReportRow{{Columns: map[string]any{"other": "1763607600"}}}, time.Time{}, "row 1 missing or not a string"},
		{"json_number_not_converted", named, rows(1763607600.0), time.Time{}, "row 1 missing or not a string"},
		{"zero_epoch", named, rows("0"), time.Time{}, "row 1 not a positive epoch"},
		{"negative_epoch", named, rows("-5"), time.Time{}, "row 1 not a positive epoch"},
		{"epoch_milliseconds", named, rows("1763607600000"), time.Time{}, "row 1 epoch over 1e11 (milliseconds?)"},
		{"epoch_at_1e11_is_still_seconds_but_future", named, rows("100000000000"), time.Time{}, "row 1 more than 1 h in the future"},
		{"epoch_seconds_in_the_future", named, rows("9999999999"), time.Time{}, "row 1 more than 1 h in the future"},
		{"rfc3339_just_inside_the_hour", named, rows("2026-11-22T13:00:00Z"), time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC), ""},
		{"rfc3339_just_past_the_hour", named, rows("2026-11-22T13:00:01Z"), time.Time{}, "row 1 more than 1 h in the future"},
		{"future_row_skipped_oldest_kept", named, rows("2026-11-23T00:00:00Z", "1763000000"), time.Unix(1763000000, 0).UTC(), "1 of 2 rows unread"},
		{"date_without_zone", named, rows("2026-11-20T03:00:00"), time.Time{}, "row 1 not RFC 3339 or epoch seconds"},
		{"no_rows", named, nil, time.Time{}, "antimalware definitions report: no result rows"},
		{"name_differs_only_by_case", []osqueryposture.HostReportSummary{{ID: 9, Name: "av"}}, rows("1763607600"), time.Time{}, `no report named "AV" on this host`},
		{"two_reports_share_the_name", []osqueryposture.HostReportSummary{{ID: 9, Name: "AV"}, {ID: 10, Name: "AV"}}, rows("1763607600"), time.Time{}, `2 reports named "AV"; not read`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{
				hosts:   []osqueryposture.HostListEntry{{ID: 1, UUID: "u", Hostname: "h"}},
				detail:  map[uint64]osqueryposture.HostDetail{1: {ID: 1, UUID: "u"}},
				reports: map[uint64][]osqueryposture.HostReportSummary{1: tc.reports},
				reportRows: map[[2]uint64][]osqueryposture.HostReportRow{
					{1, 9}: tc.rows, {1, 10}: tc.rows,
				},
			}
			out, err := osqueryposture.PullFromFleet(context.Background(), api, func() time.Time { return pull }, osqueryposture.FleetPolicies{DefinitionsReport: "AV", DefinitionsColumn: col})
			if err != nil || len(out) != 1 {
				t.Fatalf("PullFromFleet: %v (%d rows)", err, len(out))
			}
			got := out[0]
			switch {
			case tc.reason == "" && strings.Contains(got.Reason, "antimalware definitions report"):
				t.Errorf("reason = %q; want no report note", got.Reason)
			case tc.reason != "" && !strings.Contains(got.Reason, tc.reason):
				t.Errorf("reason = %q; want it to contain %q", got.Reason, tc.reason)
			}
			if !got.AntimalwareDefinitionsUpdatedAt.Equal(tc.want) {
				t.Errorf("definitions = %v; want %v", got.AntimalwareDefinitionsUpdatedAt, tc.want)
			}
			if wantSrc := map[bool]string{true: "", false: "fleet_report:AV"}[tc.want.IsZero()]; got.AntimalwareDefinitionsSource != wantSrc {
				t.Errorf("source = %q; want %q", got.AntimalwareDefinitionsSource, wantSrc)
			}
		})
	}
}

// #2936 review: the reports list is followed through meta.has_next_results,
// so a report on page 2 is found.
func TestFleetClient_ListHostReportsFollowsPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "0":
			_, _ = w.Write([]byte(`{"reports": [{"id": 1, "name": "AV definitions old"}], "meta": {"has_next_results": true, "has_previous_results": false}, "count": 2}`))
		case "1":
			_, _ = w.Write([]byte(`{"reports": [{"id": 2, "name": "AV definitions"}], "meta": {"has_next_results": false, "has_previous_results": true}, "count": 2}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	creds, _ := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "x"})
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	got, err := client.ListHostReports(context.Background(), 7, "AV definitions")
	if err != nil {
		t.Fatalf("ListHostReports: %v", err)
	}
	if len(got) != 2 || got[1].ID != 2 || got[1].Name != "AV definitions" {
		t.Errorf("reports = %+v; want both pages", got)
	}
	if strings.Join(pages, ",") != "0,1" {
		t.Errorf("pages requested = %v; want 0,1", pages)
	}
}

// #2936 review: a server that always reports another page is cut off with
// an error, not followed forever.
func TestFleetClient_ListHostReportsBoundsPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"reports": [], "meta": {"has_next_results": true}}`))
	}))
	t.Cleanup(srv.Close)
	creds, _ := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "x"})
	client := osqueryposture.NewFleetClient(srv.Client(), srv.URL, creds)
	if _, err := client.ListHostReports(context.Background(), 7, "AV"); err == nil {
		t.Fatal("expected an error from an endless page sequence")
	}
	if calls != 50 {
		t.Errorf("calls = %d; want 50", calls)
	}
}

// #2936: without a named report the connector makes no report calls.
func TestPullFromFleet_NoDefinitionsReportNoCalls(t *testing.T) {
	api := &fakeAPI{
		hosts:  []osqueryposture.HostListEntry{{ID: 1, UUID: "u", Hostname: "h"}},
		detail: map[uint64]osqueryposture.HostDetail{1: {ID: 1, UUID: "u"}},
	}
	if _, err := osqueryposture.PullFromFleet(context.Background(), api, nil, osqueryposture.FleetPolicies{}); err != nil {
		t.Fatalf("PullFromFleet: %v", err)
	}
	if api.reportCalls != 0 {
		t.Errorf("report calls = %d; want 0", api.reportCalls)
	}
}

// ---- Fakes ----

type fakeAPI struct {
	hosts       []osqueryposture.HostListEntry
	detail      map[uint64]osqueryposture.HostDetail
	err         error
	reports     map[uint64][]osqueryposture.HostReportSummary
	reportRows  map[[2]uint64][]osqueryposture.HostReportRow
	reportCalls int
}

func (f *fakeAPI) ListHostReports(_ context.Context, hostID uint64, _ string) ([]osqueryposture.HostReportSummary, error) {
	f.reportCalls++
	return f.reports[hostID], nil
}

func (f *fakeAPI) GetHostReport(_ context.Context, hostID, reportID uint64) (*osqueryposture.HostReportData, error) {
	f.reportCalls++
	return &osqueryposture.HostReportData{Results: f.reportRows[[2]uint64{hostID, reportID}]}, nil
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
