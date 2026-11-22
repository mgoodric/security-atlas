package schemaregistry

import (
	"context"
	"testing"
)

// #2935: monitoring.host_capacity.v1 1.0.0, shaped as the Datadog
// connector's hostcapacity.Build emits it.
func TestMonitoringHostCapacity100_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	const base = `"source_vendor":"datadog","host_name":"web-1","host_role":"production","host_role_source":"datadog_scope:env:production","window_start":"2026-11-21T11:55:00Z","window_end":"2026-11-21T12:00:00Z"`
	cases := []struct {
		name    string
		payload string
		ok      bool
	}{
		{"all_readings", `{` + base + `,"cpu_sample_count":20,"memory_used_percent":60,"cpu_p95_5m_percent":76,"disk_used_percent":80}`, true},
		{"no_readings", `{` + base + `,"cpu_sample_count":6}`, true},
		{"boundaries", `{` + base + `,"memory_used_percent":0,"cpu_p95_5m_percent":100,"disk_used_percent":100}`, true},
		{"missing_host_name", `{"source_vendor":"datadog","host_role":"production","host_role_source":"x","window_start":"2026-11-21T11:55:00Z","window_end":"2026-11-21T12:00:00Z"}`, false},
		{"empty_host_name", `{` + base + `,"host_name":""}`, false},
		{"other_vendor", `{"source_vendor":"grafana","host_name":"web-1","host_role":"production","host_role_source":"x","window_start":"2026-11-21T11:55:00Z","window_end":"2026-11-21T12:00:00Z"}`, false},
		{"other_role", `{"source_vendor":"datadog","host_name":"web-1","host_role":"staging","host_role_source":"x","window_start":"2026-11-21T11:55:00Z","window_end":"2026-11-21T12:00:00Z"}`, false},
		{"memory_over_100", `{` + base + `,"memory_used_percent":100.5}`, false},
		{"cpu_negative", `{` + base + `,"cpu_p95_5m_percent":-1}`, false},
		{"disk_as_string", `{` + base + `,"disk_used_percent":"80"}`, false},
		{"samples_fractional", `{` + base + `,"cpu_sample_count":2.5}`, false},
		{"unknown_field", `{` + base + `,"host_uuid":"abc"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "monitoring.host_capacity.v1", "1.0.0", []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}
