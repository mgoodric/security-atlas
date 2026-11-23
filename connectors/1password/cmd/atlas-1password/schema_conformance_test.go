package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opaccount"
)

// TestBuildOrgPolicyRecord_PayloadConformsToV2 runs every shape the
// connector emits through the check the DB-backed ingest stage applies to
// every push: protojson-marshal the payload, then ValidatePayload against
// the registered schema for the record's kind and schema_version. The
// in-memory server the other tests push to only checks registration.
func TestBuildOrgPolicyRecord_PayloadConformsToV2(t *testing.T) {
	t.Parallel()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	at := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	cases := map[string]opaccount.PolicyState{
		"audit_events_on": {
			OrgID: "M4E2SWNZAZFTRGQGDNS2E5A4MU", AuditEventsEnabled: true,
			ActiveMembers: 2, SuspendedMembers: 1, Result: opaccount.ResultInconclusive, ObservedAt: at,
		},
		"audit_events_off_empty_account": {
			OrgID: "M4E2SWNZAZFTRGQGDNS2E5A4MU", Result: opaccount.ResultFail, ObservedAt: at,
		},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec, err := buildOrgPolicyRecord(&s, "prod", "")
			if err != nil {
				t.Fatalf("buildOrgPolicyRecord: %v", err)
			}
			body, err := protojson.Marshal(rec.GetPayload())
			if err != nil {
				t.Fatalf("protojson.Marshal: %v", err)
			}
			if err := reg.ValidatePayload(context.Background(), tenantA, rec.GetEvidenceKind(), rec.GetSchemaVersion(), body); err != nil {
				t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
			}
		})
	}
}

// The v1 schema still validates old records, and a v2 payload is not a
// valid v1 payload: the versions are distinct shapes.
func TestOrgPolicyV2_IsNotAV1Payload(t *testing.T) {
	t.Parallel()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	body := []byte(`{"org_id":"X","audit_events_enabled":true,"active_members":1,"suspended_members":0}`)
	if err := reg.ValidatePayload(context.Background(), tenantA, "1password.org_policy.v1", "1.0.0", body); err == nil {
		t.Fatal("a v2 payload validated as v1")
	}
}
