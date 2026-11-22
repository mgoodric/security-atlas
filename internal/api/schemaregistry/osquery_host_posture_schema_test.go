package schemaregistry

import (
	"context"
	"io/fs"
	"testing"
)

func readHostPosture(t *testing.T, v string) []byte {
	t.Helper()
	b, err := fs.ReadFile(PlatformSchemasFS(), "osquery.host_posture/"+v+".json")
	if err != nil {
		t.Fatalf("read %s: %v", v, err)
	}
	return b
}

// #2912: osquery.host_posture 1.1.0 adds last_seen_at + reason and must stay
// additive over 1.0.0, so 1.0.0 payloads keep validating under kind .v1.
func TestOsqueryHostPosture110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readHostPosture(t, "1.0.0"), readHostPosture(t, "1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}

// #2921: 1.2.0 adds firewall_source + screen_lock_source and must stay
// additive over 1.1.0.
func TestOsqueryHostPosture120_AdditiveOver110(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readHostPosture(t, "1.1.0"), readHostPosture(t, "1.2.0")); err != nil {
		t.Fatalf("1.2.0 is not additive over 1.1.0: %v", err)
	}
}

// #2826: 1.3.0 adds capacity, host_role, antimalware and EDR and must stay
// additive over 1.2.0, so records already pushed at 1.2.0 stay valid under
// their own version.
func TestOsqueryHostPosture130_AdditiveOver120(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readHostPosture(t, "1.2.0"), readHostPosture(t, "1.3.0")); err != nil {
		t.Fatalf("1.3.0 is not additive over 1.2.0: %v", err)
	}
}

// #2826: under 1.3.0 a *_source is valid only alongside its value
// (dependentRequired), the new fields are range- and pattern-checked, and a
// 1.2.0 payload still validates under 1.2.0.
func TestOsqueryHostPosture130_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name    string
		version string
		payload string
		ok      bool
	}{
		{"all_new_fields", "1.3.0", `{"host_uuid":"u","hostname":"h","disk_used_percent":29,"memory_used_percent":41.5,"cpu_p95_5m_percent":12,"host_role":"production","host_role_source":"fleet_label:Production","antimalware_protected":true,"antimalware_source":"fleet_policy:AV","edr_healthy":false,"edr_source":"fleet_policy:EDR"}`, true},
		{"none_of_the_new_fields", "1.3.0", `{"host_uuid":"u","hostname":"h"}`, true},
		{"value_without_source", "1.3.0", `{"host_uuid":"u","hostname":"h","edr_healthy":true}`, true},
		{"antimalware_source_alone", "1.3.0", `{"host_uuid":"u","hostname":"h","antimalware_source":"fleet_policy:AV"}`, false},
		{"edr_source_alone", "1.3.0", `{"host_uuid":"u","hostname":"h","edr_source":"fleet_policy:EDR"}`, false},
		{"host_role_source_alone", "1.3.0", `{"host_uuid":"u","hostname":"h","host_role_source":"fleet_label:Production"}`, false},
		{"firewall_source_alone", "1.3.0", `{"host_uuid":"u","hostname":"h","firewall_source":"fleet_policy:FW"}`, false},
		{"screen_lock_source_alone", "1.3.0", `{"host_uuid":"u","hostname":"h","screen_lock_source":"fleet_policy:SL"}`, false},
		{"disk_over_100", "1.3.0", `{"host_uuid":"u","hostname":"h","disk_used_percent":101}`, false},
		{"memory_negative", "1.3.0", `{"host_uuid":"u","hostname":"h","memory_used_percent":-1}`, false},
		{"unknown_role", "1.3.0", `{"host_uuid":"u","hostname":"h","host_role":"staging"}`, false},
		{"role_source_wrong_prefix", "1.3.0", `{"host_uuid":"u","hostname":"h","host_role":"production","host_role_source":"fleet_policy:x"}`, false},
		{"v120_payload_under_v120", "1.2.0", `{"host_uuid":"u","hostname":"h","firewall_enabled":true,"firewall_source":"fleet_policy:FW","last_seen_at":"2026-11-21T10:00:00Z"}`, true},
		{"v120_rejects_new_fields", "1.2.0", `{"host_uuid":"u","hostname":"h","edr_healthy":true}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "osquery.host_posture.v1", tc.version, []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}
