package hostcapacity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fixtures in testdata/ are built to Datadog's documented /api/v1/query
// response shape. They are NOT recorded from a live tenant (testdata/README.md).

var fixtureNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

var fixtureFiles = map[string]string{
	"system.cpu.idle":       "query_cpu_idle.json",
	"system.mem.pct_usable": "query_mem_pct_usable.json",
	"system.disk.used":      "query_disk_used.json",
	"system.disk.total":     "query_disk_total.json",
}

// fixtureServer serves the testdata response for each query's metric and
// records every request it sees.
func fixtureServer(t *testing.T) (*Client, *[]*http.Request) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []*http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Clone(context.Background()))
		mu.Unlock()
		q := r.URL.Query().Get("query")
		for metric, file := range fixtureFiles {
			if strings.Contains(q, ":"+metric+"{") {
				b, err := os.ReadFile(filepath.Join("testdata", file))
				if err != nil {
					t.Errorf("read %s: %v", file, err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
				return
			}
		}
		t.Errorf("unexpected query %q", q)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.Client(), srv.URL, "test-api-key", "test-app-key"), &seen
}

func fptr(v float64) *float64 { return &v }

func TestCollect_DocumentedShapeFixtures(t *testing.T) {
	t.Parallel()
	c, seen := fixtureServer(t)
	hosts, err := Collect(context.Background(), c, "env:production", func() time.Time { return fixtureNow })
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %d, want 2: %+v", len(hosts), hosts)
	}

	w1, w2 := hosts[0], hosts[1]
	if w1.HostName != "web-1" || w2.HostName != "web-2" {
		t.Fatalf("host order = %s, %s", w1.HostName, w2.HostName)
	}
	check := func(name string, got, want *float64) {
		t.Helper()
		switch {
		case want == nil && got != nil:
			t.Errorf("%s = %v, want unread", name, *got)
		case want != nil && got == nil:
			t.Errorf("%s unread, want %v", name, *want)
		case want != nil && *got != *want:
			t.Errorf("%s = %v, want %v", name, *got, *want)
		}
	}
	// web-1: 20 CPU points, used 4..80, nearest-rank p95 = 76. Latest
	// non-null pct_usable is 0.40 (the trailing null is skipped). Disks are
	// 40% and 80%; the fuller one counts.
	check("web-1 memory", w1.MemoryUsedPercent, fptr(60))
	check("web-1 cpu p95", w1.CPUP95Percent, fptr(76))
	check("web-1 disk", w1.DiskUsedPercent, fptr(80))
	if w1.CPUSampleCount != 20 {
		t.Errorf("web-1 cpu samples = %d, want 20", w1.CPUSampleCount)
	}
	// web-2: 6 CPU points is under MinCPUSamples, so no p95. Its disk has
	// used bytes but no total, so no disk reading.
	check("web-2 memory", w2.MemoryUsedPercent, fptr(95))
	check("web-2 cpu p95", w2.CPUP95Percent, nil)
	check("web-2 disk", w2.DiskUsedPercent, nil)
	if w2.CPUSampleCount != 6 {
		t.Errorf("web-2 cpu samples = %d, want 6", w2.CPUSampleCount)
	}

	wantStart := fixtureNow.Add(-Window)
	if !w1.WindowStart.Equal(wantStart) || !w1.WindowEnd.Equal(fixtureNow) {
		t.Errorf("window = %s..%s", w1.WindowStart, w1.WindowEnd)
	}

	if len(*seen) != 4 {
		t.Fatalf("requests = %d, want 4", len(*seen))
	}
	want := Queries("env:production")
	for i, r := range *seen {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/query" {
			t.Errorf("request %d = %s %s, want GET /api/v1/query", i, r.Method, r.URL.Path)
		}
		if r.Header.Get("DD-API-KEY") != "test-api-key" || r.Header.Get("DD-APPLICATION-KEY") != "test-app-key" {
			t.Errorf("request %d: auth headers not set", i)
		}
		q := r.URL.Query()
		if q.Get("query") != want[i] {
			t.Errorf("request %d query = %q, want %q", i, q.Get("query"), want[i])
		}
		if q.Get("from") != strconv.FormatInt(wantStart.Unix(), 10) || q.Get("to") != strconv.FormatInt(fixtureNow.Unix(), 10) {
			t.Errorf("request %d from/to = %s/%s", i, q.Get("from"), q.Get("to"))
		}
	}
}

func TestQueries_Shape(t *testing.T) {
	t.Parallel()
	got := Queries("env:production,team:core")
	want := [4]string{
		"avg:system.cpu.idle{env:production,team:core} by {host}.rollup(avg, 15)",
		"avg:system.mem.pct_usable{env:production,team:core} by {host}.rollup(avg, 15)",
		"avg:system.disk.used{env:production,team:core} by {host,device}.rollup(avg, 15)",
		"avg:system.disk.total{env:production,team:core} by {host,device}.rollup(avg, 15)",
	}
	if got != want {
		t.Errorf("Queries = %q", got)
	}
}

func TestCheckScope(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"env:production", "env:prod,team:core", "role:db-primary", "kube_cluster:eu/west-1.a"} {
		if err := CheckScope(ok); err != nil {
			t.Errorf("CheckScope(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "env:prod}", "env:prod} by {host", "env:prod OR env:dev", "*", "env:prod,", ",env:prod", "!env:dev"} {
		if err := CheckScope(bad); err == nil {
			t.Errorf("CheckScope(%q) = nil, want an error", bad)
		}
	}
}

func TestP95_NearestRank(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{5}, 5},
		{[]float64{1, 2}, 2},
		{[]float64{10, 1, 9, 2, 8, 3, 7, 4, 6, 5}, 10}, // ceil(9.5)=10th
		{seq(20), 19},                                  // ceil(19)=19th
		{seq(21), 20},                                  // ceil(19.95)=20th
		{seq(100), 95},                                 // ceil(95)=95th
	}
	for _, tc := range cases {
		if got := p95(tc.in); got != tc.want {
			t.Errorf("p95(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func seq(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(n - i) // descending, so p95 must sort
	}
	return out
}

type fakeAPI struct {
	series map[string][]Series
	err    error
}

func (f fakeAPI) Query(_ context.Context, _, _ time.Time, query string) ([]Series, error) {
	if f.err != nil {
		return nil, f.err
	}
	for metric, s := range f.series {
		if strings.Contains(query, ":"+metric+"{") {
			return s, nil
		}
	}
	return nil, nil
}

func pts(vals ...any) []Point {
	out := make([]Point, len(vals))
	for i, v := range vals {
		out[i].At = fixtureNow.Add(time.Duration(i) * 15 * time.Second)
		if f, ok := v.(float64); ok {
			out[i].Value = &f
		}
	}
	return out
}

func host(name string, extra ...string) map[string]string {
	m := map[string]string{"host": name}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

func TestCollect_UnreadableReadingsAreLeftOut(t *testing.T) {
	t.Parallel()
	ten := make([]any, 10)
	for i := range ten {
		ten[i] = 50.0
	}
	api := fakeAPI{series: map[string][]Series{
		"system.cpu.idle": {
			{Tags: host("exact-min"), Points: pts(ten...)},         // 10 points: a p95
			{Tags: host("bad-cpu"), Points: pts(101.0, -1.0, nil)}, // out of range only
			{Tags: map[string]string{}, Points: pts(ten...)},       // no host tag: ignored
		},
		"system.mem.pct_usable": {
			{Tags: host("exact-min"), Points: pts(1.5)}, // not a fraction
			{Tags: host("mem-zero"), Points: pts(0.0)},  // fully used
			{Tags: host("mem-null"), Points: pts(nil)},  // gap only
			{Tags: host("mem-full"), Points: pts(1.0)},  // nothing used
		},
		"system.disk.used": {
			{Tags: host("exact-min", "device", "/a"), Points: pts(50.0)},
			{Tags: host("exact-min", "device", "/b"), Points: pts(30.0)},
			{Tags: host("disk-over-total", "device", "/a"), Points: pts(200.0)},
			{Tags: host("disk-zero-total", "device", "/a"), Points: pts(1.0)},
			{Tags: host("disk-unread-first", "device", "/a"), Points: pts(nil)},
			{Tags: host("disk-unread-first", "device", "/b"), Points: pts(10.0)},
		},
		"system.disk.total": {
			{Tags: host("exact-min", "device", "/a"), Points: pts(100.0)},
			{Tags: host("exact-min", "device", "/b"), Points: pts(40.0)},
			{Tags: host("disk-over-total", "device", "/a"), Points: pts(100.0)},
			{Tags: host("disk-zero-total", "device", "/a"), Points: pts(0.0)},
			{Tags: host("disk-unread-first", "device", "/a"), Points: pts(100.0)},
			{Tags: host("disk-unread-first", "device", "/b"), Points: pts(100.0)},
		},
	}}
	hosts, err := Collect(context.Background(), api, "env:production", func() time.Time { return fixtureNow })
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	by := map[string]Host{}
	for _, h := range hosts {
		by[h.HostName] = h
	}
	if _, ok := by[""]; ok {
		t.Error("a series without a host tag produced a host")
	}
	if h := by["exact-min"]; h.CPUP95Percent == nil || *h.CPUP95Percent != 50 {
		t.Errorf("exact-min cpu = %v, want 50 from exactly MinCPUSamples points", h.CPUP95Percent)
	}
	if h := by["exact-min"]; h.MemoryUsedPercent != nil {
		t.Errorf("exact-min memory = %v, want unread for a fraction over 1", *h.MemoryUsedPercent)
	}
	if h := by["exact-min"]; h.DiskUsedPercent == nil || *h.DiskUsedPercent != 75 {
		t.Errorf("exact-min disk = %v, want 75 (the fuller device: 30/40)", h.DiskUsedPercent)
	}
	if h := by["bad-cpu"]; h.CPUP95Percent != nil || h.CPUSampleCount != 0 {
		t.Errorf("bad-cpu = %v/%d, want unread with 0 samples", h.CPUP95Percent, h.CPUSampleCount)
	}
	if h := by["mem-zero"]; h.MemoryUsedPercent == nil || *h.MemoryUsedPercent != 100 {
		t.Errorf("mem-zero = %v, want 100", h.MemoryUsedPercent)
	}
	if h := by["mem-full"]; h.MemoryUsedPercent == nil || *h.MemoryUsedPercent != 0 {
		t.Errorf("mem-full = %v, want 0", h.MemoryUsedPercent)
	}
	if h := by["mem-null"]; h.MemoryUsedPercent != nil {
		t.Errorf("mem-null = %v, want unread", *h.MemoryUsedPercent)
	}
	for _, name := range []string{"disk-over-total", "disk-zero-total", "disk-unread-first"} {
		if h := by[name]; h.DiskUsedPercent != nil {
			t.Errorf("%s disk = %v, want unread", name, *h.DiskUsedPercent)
		}
	}
}

func TestDiskUsed_UnreadDeviceAfterReadOneUnreadsHost(t *testing.T) {
	t.Parallel()
	used := []Series{
		{Tags: host("h", "device", "/a"), Points: pts(10.0)},
		{Tags: host("h", "device", "/b"), Points: pts(99.0)},
	}
	total := []Series{{Tags: host("h", "device", "/a"), Points: pts(100.0)}}
	if got := diskUsed(used, total)["h"]; got != nil {
		t.Errorf("disk = %v, want unread: /b has no total and could be the full one", *got)
	}
}

func TestCollect_Errors(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, "env:production", nil); err == nil {
		t.Error("nil API: want an error")
	}
	if _, err := Collect(context.Background(), fakeAPI{}, "env:prod}", nil); err == nil {
		t.Error("bad scope: want an error")
	}
	boom := errors.New("boom")
	if _, err := Collect(context.Background(), fakeAPI{err: boom}, "env:production", nil); !errors.Is(err, boom) {
		t.Errorf("query error = %v, want boom", err)
	}
}

func TestCollect_HostCap(t *testing.T) {
	t.Parallel()
	many := make([]Series, maxHosts+1)
	for i := range many {
		many[i] = Series{Tags: host("h" + strconv.Itoa(i)), Points: pts(0.5)}
	}
	api := fakeAPI{series: map[string][]Series{"system.mem.pct_usable": many}}
	if _, err := Collect(context.Background(), api, "env:production", nil); err == nil || !strings.Contains(err.Error(), "per-run cap") {
		t.Errorf("err = %v, want the per-run cap", err)
	}
}

func TestClient_Errors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query") {
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["Forbidden"]}`))
		case "empty403":
			w.WriteHeader(http.StatusForbidden)
		case "status-error":
			_, _ = w.Write([]byte(`{"status":"error","error":"Rate limit"}`))
		case "garbage":
			_, _ = w.Write([]byte(`not json`))
		default:
			_, _ = w.Write([]byte(`{"status":"ok","series":[{"scope":"host:a","pointlist":[[1795262100000,1],[null,2],[1795262115000],[1795262130000,null]]}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(nil, srv.URL+"/", "k", "a")
	ctx := context.Background()

	var apiErr *APIError
	if _, err := c.Query(ctx, fixtureNow, fixtureNow, "forbidden"); !errors.As(err, &apiErr) || apiErr.Status != 403 || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("forbidden: %v", err)
	}
	if _, err := c.Query(ctx, fixtureNow, fixtureNow, "empty403"); err == nil || err.Error() != "datadog: HTTP 403" {
		t.Errorf("empty403: %v", err)
	}
	if _, err := c.Query(ctx, fixtureNow, fixtureNow, "status-error"); err == nil || !strings.Contains(err.Error(), "Rate limit") {
		t.Errorf("status-error: %v", err)
	}
	if _, err := c.Query(ctx, fixtureNow, fixtureNow, "garbage"); err == nil {
		t.Error("garbage: want a decode error")
	}
	s, err := c.Query(ctx, fixtureNow, fixtureNow, "ok")
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(s) != 1 || s[0].Tags["host"] != "a" || len(s[0].Points) != 2 {
		t.Fatalf("ok series = %+v, want host a with 2 points (malformed points dropped)", s)
	}
	if s[0].Points[1].Value != nil {
		t.Error("a null value must decode as nil")
	}

	bad := NewClient(nil, "http://127.0.0.1:1", "k", "a")
	if _, err := bad.Query(ctx, fixtureNow, fixtureNow, "x"); err == nil {
		t.Error("unreachable: want an error")
	}
	if _, err := NewClient(nil, "://bad", "k", "a").Query(ctx, fixtureNow, fixtureNow, "x"); err == nil {
		t.Error("bad base URL: want an error")
	}
}

func TestParseScope(t *testing.T) {
	t.Parallel()
	got := parseScope("device:/dev/sda1, host:web-1,flag,")
	if got["host"] != "web-1" || got["device"] != "/dev/sda1" {
		t.Errorf("parseScope = %v", got)
	}
	if v, ok := got["flag"]; !ok || v != "" {
		t.Errorf("bare tag = %q, %v", v, ok)
	}
}
