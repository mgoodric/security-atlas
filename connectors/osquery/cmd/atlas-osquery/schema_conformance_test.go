package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryposture"
)

func ptr(b bool) *bool { return &b }

func f64(v float64) *float64 { return &v }

// validatePushed runs a record built by buildHostPostureRecord through the
// check the DB-backed ingest stage applies to every push: protojson-marshal
// the payload, then ValidatePayload against the registered schema for the
// record's kind and schema_version. The in-memory platform the other tests
// push to only checks registration (#2815).
func validatePushed(t *testing.T, p osqueryposture.HostPosture) (map[string]any, error) {
	t.Helper()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	rec, err := buildHostPostureRecord(p, "example", "prod", "scf:END-03")
	if err != nil {
		t.Fatalf("buildHostPostureRecord: %v", err)
	}
	payloadJSON, err := protojson.Marshal(rec.GetPayload())
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return rec.GetPayload().AsMap(),
		reg.ValidatePayload(context.Background(), tenantA, rec.GetEvidenceKind(), rec.GetSchemaVersion(), payloadJSON)
}

// TestBuildHostPostureRecord_ConformsAndOmitsUnread covers the record shapes
// the connector emits: a Fleet host (screen lock and firewall unread), a
// host whose detail fetch failed (nothing read), and a local host with
// every field read. Each must validate, and no unread field may appear in
// the payload as false (#2912).
func TestBuildHostPostureRecord_ConformsAndOmitsUnread(t *testing.T) {
	pull := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	seen := time.Date(2026, 5, 9, 8, 30, 0, 0, time.UTC)
	cases := map[string]struct {
		in      osqueryposture.HostPosture
		present []string
		absent  []string
	}{
		"fleet_host": {
			in: osqueryposture.HostPosture{
				HostUUID: "u1", Hostname: "mac-1", Platform: "darwin", OSVersion: "macOS 15.2",
				DiskEncryptionEnabled: ptr(true), MDMEnrolled: ptr(true), LastSeen: seen,
				Result: osqueryposture.ResultInconclusive, Reason: "screen_lock not reported", ObservedAt: pull,
			},
			present: []string{"disk_encryption_enabled", "mdm_enrolled", "last_seen_at", "reason"},
			absent:  []string{"screen_lock_enabled", "firewall_enabled", "firewall_source", "screen_lock_source"},
		},
		// #2921: firewall and screen lock read from named Fleet policies
		// carry their source.
		"fleet_host_named_policies": {
			in: osqueryposture.HostPosture{
				HostUUID: "u4", Hostname: "mac-2", Platform: "darwin",
				DiskEncryptionEnabled: ptr(true), MDMEnrolled: ptr(true),
				FirewallEnabled: ptr(true), FirewallSource: "fleet_policy:Firewall enabled (macOS)",
				ScreenLockEnabled: ptr(false), ScreenLockSource: "fleet_policy:Screen lock enabled (macOS)",
				LastSeen: seen, Result: osqueryposture.ResultFail, Reason: "screen_lock_disabled", ObservedAt: pull,
			},
			present: []string{"firewall_enabled", "firewall_source", "screen_lock_enabled", "screen_lock_source"},
		},
		// A source without its boolean (the policy had no result) is not
		// emitted on its own.
		"source_without_value": {
			in: osqueryposture.HostPosture{
				HostUUID: "u5", Hostname: "mac-3", FirewallSource: "fleet_policy:x", ObservedAt: pull,
			},
			absent: []string{"firewall_enabled", "firewall_source", "screen_lock_enabled", "screen_lock_source"},
		},
		// #2826: disk use, host role, antimalware and EDR, each with its
		// source, validate under 1.3.0.
		"fleet_host_capacity_role_antimalware_edr": {
			in: osqueryposture.HostPosture{
				HostUUID: "u6", Hostname: "srv-1", Platform: "linux",
				DiskUsedPercent: f64(29), HostRole: "production", HostRoleSource: "fleet_label:Production",
				AntimalwareProtected: ptr(true), AntimalwareSource: "fleet_policy:Anti-malware current",
				EDRHealthy: ptr(false), EDRSource: "fleet_policy:EDR healthy",
				LastSeen: seen, Result: osqueryposture.ResultInconclusive, Reason: "disk_encryption and screen_lock not reported", ObservedAt: pull,
			},
			present: []string{"disk_used_percent", "host_role", "host_role_source", "antimalware_protected", "antimalware_source", "edr_healthy", "edr_source"},
			absent:  []string{"memory_used_percent", "cpu_p95_5m_percent"},
		},
		// A policy source without its result is not emitted on its own:
		// 1.3.0's dependentRequired would reject it.
		"antimalware_edr_source_without_value": {
			in: osqueryposture.HostPosture{
				HostUUID: "u7", Hostname: "srv-2", AntimalwareSource: "fleet_policy:a", EDRSource: "fleet_policy:e", ObservedAt: pull,
			},
			absent: []string{"antimalware_protected", "antimalware_source", "edr_healthy", "edr_source", "host_role", "host_role_source", "disk_used_percent"},
		},
		"detail_fetch_failed": {
			in: osqueryposture.HostPosture{
				HostUUID: "u2", Hostname: "x", Platform: "darwin", LastSeen: seen,
				Result: osqueryposture.ResultInconclusive, Reason: "detail fetch failed: fleet: HTTP 503", ObservedAt: pull,
			},
			present: []string{"last_seen_at", "reason"},
			absent:  []string{"disk_encryption_enabled", "screen_lock_enabled", "firewall_enabled", "mdm_enrolled"},
		},
		"local_all_read": {
			in: osqueryposture.HostPosture{
				HostUUID: "u3", Hostname: "dev", Platform: "darwin",
				DiskEncryptionEnabled: ptr(true), ScreenLockEnabled: ptr(true),
				FirewallEnabled: ptr(false), MDMEnrolled: ptr(false), LastSeen: pull,
				Result: osqueryposture.ResultPass, Reason: "disk_encryption+screen_lock", ObservedAt: pull,
			},
			present: []string{"disk_encryption_enabled", "screen_lock_enabled", "firewall_enabled", "mdm_enrolled", "last_seen_at"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			payload, err := validatePushed(t, tc.in)
			if err != nil {
				t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
			}
			for _, k := range tc.present {
				if _, ok := payload[k]; !ok {
					t.Errorf("payload missing %q: %v", k, payload)
				}
			}
			for _, k := range tc.absent {
				if v, ok := payload[k]; ok {
					t.Errorf("payload carries unread %q = %v", k, v)
				}
			}
		})
	}
}

// last_seen_at is the host's check-in time; observed_at stays the pull time,
// because ingest rejects an observed_at more than 24h from receipt.
func TestBuildHostPostureRecord_LastSeenIsNotObservedAt(t *testing.T) {
	pull := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	seen := time.Date(2026, 4, 1, 9, 15, 0, 0, time.UTC)
	rec, err := buildHostPostureRecord(osqueryposture.HostPosture{
		HostUUID: "u", Hostname: "h", LastSeen: seen, ObservedAt: pull,
	}, "o", "e", "scf:END-03")
	if err != nil {
		t.Fatalf("buildHostPostureRecord: %v", err)
	}
	if got := rec.GetPayload().AsMap()["last_seen_at"]; got != "2026-04-01T09:15:00Z" {
		t.Errorf("last_seen_at = %v; want 2026-04-01T09:15:00Z", got)
	}
	if got := rec.GetObservedAt().AsTime(); !got.Equal(pull) {
		t.Errorf("observed_at = %v; want pull time %v", got, pull)
	}
	if rec.GetSchemaVersion() != "1.3.0" {
		t.Errorf("schema_version = %q; want 1.3.0", rec.GetSchemaVersion())
	}
}
