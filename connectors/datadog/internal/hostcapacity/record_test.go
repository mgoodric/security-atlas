package hostcapacity

import (
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

func TestBuild_Payload(t *testing.T) {
	t.Parallel()
	h := Host{
		HostName:          "web-1",
		MemoryUsedPercent: fptr(60),
		CPUP95Percent:     fptr(76),
		CPUSampleCount:    20,
		DiskUsedPercent:   fptr(80),
		WindowStart:       fixtureNow.Add(-Window),
		WindowEnd:         fixtureNow.Add(7 * time.Minute),
	}
	rec, err := Build(h, "env:production", "", "connector:datadog:hostcapacity@dev", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != "monitoring.host_capacity.v1" || rec.GetSchemaVersion() != "1.0.0" {
		t.Errorf("kind/version = %s/%s", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	if rec.GetControlId() != "" {
		t.Errorf("control = %q, want empty (routed)", rec.GetControlId())
	}
	if rec.GetResult() != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %s", rec.GetResult())
	}
	if got := rec.GetObservedAt().AsTime(); !got.Equal(time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observed_at = %s, want the window end's hour", got)
	}
	p := rec.GetPayload().AsMap()
	want := map[string]any{
		"source_vendor":       "datadog",
		"host_name":           "web-1",
		"host_role":           "production",
		"host_role_source":    "datadog_scope:env:production",
		"window_start":        "2026-11-21T11:55:00Z",
		"window_end":          "2026-11-21T12:07:00Z",
		"cpu_sample_count":    float64(20),
		"memory_used_percent": float64(60),
		"cpu_p95_5m_percent":  float64(76),
		"disk_used_percent":   float64(80),
	}
	if len(p) != len(want) {
		t.Errorf("payload has %d keys, want %d: %v", len(p), len(want), p)
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("payload[%s] = %v, want %v", k, p[k], v)
		}
	}
}

func TestBuild_UnreadReadingsAreAbsent(t *testing.T) {
	t.Parallel()
	rec, err := Build(Host{HostName: "web-2", WindowStart: fixtureNow.Add(-Window), WindowEnd: fixtureNow}, "env:production", "scf:MON-02", "a", "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := rec.GetPayload().AsMap()
	for _, k := range []string{"memory_used_percent", "cpu_p95_5m_percent", "disk_used_percent"} {
		if _, ok := p[k]; ok {
			t.Errorf("%s present, want absent when unread", k)
		}
	}
	if rec.GetControlId() != "scf:MON-02" {
		t.Errorf("explicit control = %q", rec.GetControlId())
	}
}

func TestIdemKey_PerHostPerHour(t *testing.T) {
	t.Parallel()
	a := idemKey("web-1", fixtureNow)
	if a != idemKey("web-1", fixtureNow.Add(59*time.Minute)) {
		t.Error("same host, same hour: keys differ")
	}
	if a == idemKey("web-1", fixtureNow.Add(time.Hour)) {
		t.Error("next hour: same key")
	}
	if a == idemKey("web-2", fixtureNow) {
		t.Error("other host: same key")
	}
}
