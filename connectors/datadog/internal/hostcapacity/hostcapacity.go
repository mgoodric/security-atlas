// Package hostcapacity reads per-host capacity from Datadog's metrics query
// API (GET /api/v1/query, timeseries_query permission) and turns it into
// monitoring.host_capacity.v1 readings (#2935).
//
// Three readings per host, each over the last five minutes:
//
//   - memory_used_percent: (1 - system.mem.pct_usable) * 100, from the latest
//     point. The System check documents pct_usable as "usable physical RAM as
//     a fraction of the total".
//   - cpu_p95_5m_percent: the nearest-rank 95th percentile of
//     100 - system.cpu.idle over the window's points. Datadog documents
//     percentiles for distribution metrics only, and system.cpu.idle is a
//     gauge, so the percentile is taken here. The query asks for a 15-second
//     rollup, the Agent's system-metric collection interval, which gives about
//     20 points per host.
//   - disk_used_percent: the highest system.disk.used / system.disk.total *
//     100 over the host's devices, from each device's latest point. The two
//     byte metrics are used rather than system.disk.in_use, whose docs call
//     it both "a percent of the total" and "Shown as fraction".
//
// A reading that cannot be made honestly is left out, never guessed: too few
// CPU points, a fraction outside 0..1, or a device with used bytes but no
// total. A missing reading leaves the host unread, which a1_1 treats as
// inconclusive.
//
// The hosts are exactly those Datadog returns for the operator's production
// scope (--capacity-production-scope, e.g. env:production). The connector
// reads metric values for those hosts and nothing else: no tags beyond host
// and device, no dashboards, no monitors.
package hostcapacity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Window is the span every reading covers. cpu_p95_5m_percent names it.
const Window = 5 * time.Minute

// rollupSeconds is the rollup interval the queries ask for: the Agent's
// 15-second system-metric collection interval (Datadog "Data Collection and
// Resolution": System metrics, 15 seconds).
const rollupSeconds = 15

// MinCPUSamples is the fewest CPU points a p95 is taken from. A full window
// at 15 seconds is 20 points; fewer than half means the host reported for
// under half the window, and a p95 of a handful of points is not a p95.
const MinCPUSamples = 10

// maxHosts bounds one run (over-collection guard). A scope matching more
// hosts stops the run rather than pushing a partial set.
const maxHosts = 5000

// Series is one decoded series of a /api/v1/query response: its tags and its
// points, oldest first. A point's value is nil when Datadog returned null.
type Series struct {
	Tags   map[string]string
	Points []Point
}

// Point is one (time, value) sample.
type Point struct {
	At    time.Time
	Value *float64
}

// API is the narrow surface Collect depends on. Client is the live
// implementation; tests pass a fake or an httptest server.
type API interface {
	Query(ctx context.Context, from, to time.Time, query string) ([]Series, error)
}

// Host is one host's readings. A nil reading was not read.
type Host struct {
	HostName          string
	MemoryUsedPercent *float64
	CPUP95Percent     *float64
	CPUSampleCount    int
	DiskUsedPercent   *float64
	WindowStart       time.Time
	WindowEnd         time.Time
}

// scopeRe is a Datadog scope: comma-separated tags of the characters Datadog
// allows in a tag. Braces, spaces and operators are refused, so the operator's
// value cannot change the shape of the metric query it is placed in.
var scopeRe = regexp.MustCompile(`^[A-Za-z0-9_.:/\-]+(,[A-Za-z0-9_.:/\-]+)*$`)

// CheckScope reports whether scope is a usable production scope.
func CheckScope(scope string) error {
	if scope == "" {
		return errors.New("production scope is empty")
	}
	if !scopeRe.MatchString(scope) {
		return fmt.Errorf("production scope %q must be comma-separated tags (letters, digits and _ . : / -)", scope)
	}
	return nil
}

// Queries returns the four metric queries for scope, in the order Collect
// issues them: CPU idle, usable memory fraction, disk used, disk total.
func Queries(scope string) [4]string {
	r := fmt.Sprintf(".rollup(avg, %d)", rollupSeconds)
	return [4]string{
		"avg:system.cpu.idle{" + scope + "} by {host}" + r,
		"avg:system.mem.pct_usable{" + scope + "} by {host}" + r,
		"avg:system.disk.used{" + scope + "} by {host,device}" + r,
		"avg:system.disk.total{" + scope + "} by {host,device}" + r,
	}
}

// Collect reads the last Window of capacity metrics for every host in scope.
// now is injectable for tests (nil -> time.Now UTC). Hosts come back sorted
// by name.
func Collect(ctx context.Context, api API, scope string, now func() time.Time) ([]Host, error) {
	if api == nil {
		return nil, errors.New("hostcapacity: API is nil")
	}
	if err := CheckScope(scope); err != nil {
		return nil, fmt.Errorf("hostcapacity: %w", err)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	to := now().UTC().Truncate(time.Second)
	from := to.Add(-Window)

	q := Queries(scope)
	var res [4][]Series
	for i, query := range q {
		s, err := api.Query(ctx, from, to, query)
		if err != nil {
			return nil, fmt.Errorf("hostcapacity: query %d: %w", i, err)
		}
		res[i] = s
	}

	hosts := map[string]*Host{}
	get := func(name string) *Host {
		h, ok := hosts[name]
		if !ok {
			h = &Host{HostName: name, WindowStart: from, WindowEnd: to}
			hosts[name] = h
		}
		return h
	}

	for _, s := range res[0] {
		name := s.Tags["host"]
		if name == "" {
			continue
		}
		h := get(name)
		used := cpuUsed(s.Points)
		h.CPUSampleCount = len(used)
		if len(used) >= MinCPUSamples {
			p := round2(p95(used))
			h.CPUP95Percent = &p
		}
	}
	for _, s := range res[1] {
		name := s.Tags["host"]
		if name == "" {
			continue
		}
		h := get(name)
		if v, ok := latest(s.Points); ok && v >= 0 && v <= 1 {
			m := round2((1 - v) * 100)
			h.MemoryUsedPercent = &m
		}
	}
	for name, pct := range diskUsed(res[2], res[3]) {
		h := get(name)
		if pct != nil {
			p := round2(*pct)
			h.DiskUsedPercent = &p
		}
	}

	if len(hosts) > maxHosts {
		return nil, fmt.Errorf("hostcapacity: scope matches %d hosts, over the per-run cap of %d; narrow the scope", len(hosts), maxHosts)
	}
	out := make([]Host, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostName < out[j].HostName })
	return out, nil
}

// cpuUsed is 100 - idle for each non-null point with idle in 0..100.
func cpuUsed(points []Point) []float64 {
	var out []float64
	for _, p := range points {
		if p.Value == nil || math.IsNaN(*p.Value) || *p.Value < 0 || *p.Value > 100 {
			continue
		}
		out = append(out, 100-*p.Value)
	}
	return out
}

// p95 is the nearest-rank 95th percentile: the smallest value with at least
// 95% of the samples at or below it. vs must be non-empty.
func p95(vs []float64) float64 {
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	rank := int(math.Ceil(0.95 * float64(len(s))))
	return s[rank-1]
}

// latest is the newest non-null, non-NaN point's value.
func latest(points []Point) (float64, bool) {
	var (
		at  time.Time
		v   float64
		got bool
	)
	for _, p := range points {
		if p.Value == nil || math.IsNaN(*p.Value) {
			continue
		}
		if !got || !p.At.Before(at) {
			at, v, got = p.At, *p.Value, true
		}
	}
	return v, got
}

// diskUsed maps each host to its fullest device's percent used. A host with
// any device whose used bytes have no usable total maps to nil: the device
// left out could be the full one.
func diskUsed(used, total []Series) map[string]*float64 {
	type key struct{ host, device string }
	totals := map[key]float64{}
	for _, s := range total {
		k := key{s.Tags["host"], s.Tags["device"]}
		if k.host == "" {
			continue
		}
		if v, ok := latest(s.Points); ok {
			totals[k] = v
		}
	}
	out := map[string]*float64{}
	unread := map[string]bool{}
	for _, s := range used {
		k := key{s.Tags["host"], s.Tags["device"]}
		if k.host == "" {
			continue
		}
		u, okU := latest(s.Points)
		t, okT := totals[k]
		if !okU || !okT || t <= 0 || u < 0 || u > t {
			unread[k.host] = true
			out[k.host] = nil
			continue
		}
		if unread[k.host] {
			continue
		}
		pct := u / t * 100
		if cur := out[k.host]; cur == nil || pct > *cur {
			out[k.host] = &pct
		}
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// parseScope turns a series scope ("host:web-1,device:/dev/sda1") into tags.
// A tag without a colon is kept with an empty value.
func parseScope(scope string) map[string]string {
	tags := map[string]string{}
	for _, t := range strings.Split(scope, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		k, v, _ := strings.Cut(t, ":")
		tags[k] = v
	}
	return tags
}
