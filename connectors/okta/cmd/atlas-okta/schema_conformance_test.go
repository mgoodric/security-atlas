package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
)

// validateMFAPolicy runs a record built by buildMFAPolicyRecord through the
// check the DB-backed ingest stage applies to every push: protojson-marshal
// the payload, then ValidatePayload against the registered schema for the
// record's kind and schema_version. The in-memory server the other tests
// push to only checks registration (#2815 has the same test for AWS).
func validateMFAPolicy(t *testing.T, s oktapolicy.PolicyState) error {
	t.Helper()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	rec, err := buildMFAPolicyRecord(s, "example", "prod", "scf:IAC-37")
	if err != nil {
		t.Fatalf("buildMFAPolicyRecord: %v", err)
	}
	if rec.GetEvidenceKind() != "okta.mfa_policy.v2" || rec.GetSchemaVersion() != "2.0.0" {
		t.Fatalf("kind/version = %s/%s; want okta.mfa_policy.v2/2.0.0", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	payloadJSON, err := protojson.Marshal(rec.GetPayload())
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return reg.ValidatePayload(context.Background(), tenantA, rec.GetEvidenceKind(), rec.GetSchemaVersion(), payloadJSON)
}

// TestBuildMFAPolicyRecord_PayloadConformsToV2 covers every verdict the
// connector emits, and the empty-list and no-priority edges the schema's
// required arrays and minimum make sharp.
func TestBuildMFAPolicyRecord_PayloadConformsToV2(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	cases := map[string]oktapolicy.PolicyState{
		"pass_webauthn_everyone": {
			PolicyID: "00p1", PolicyName: "Default Policy", Status: "ACTIVE", Priority: 1,
			MFARequired: true, FactorsAllowed: []string{"okta_verify", "webauthn"},
			AppliesToGroups: []string{"00g1everyone"}, AppliesToEveryone: true,
			Result: oktapolicy.ResultPass, ObservedAt: at,
		},
		"fail_no_factors_no_groups_no_priority": {
			PolicyID: "00p2", PolicyName: "Empty", Status: "ACTIVE",
			Result: oktapolicy.ResultFail, ObservedAt: at,
		},
		"na_inactive": {
			PolicyID: "00p3", PolicyName: "Legacy SMS", Status: "INACTIVE", Priority: 3,
			MFARequired: true, FactorsAllowed: []string{"okta_sms"},
			AppliesToGroups: []string{"00g1everyone"}, AppliesToEveryone: true,
			Result: oktapolicy.ResultNA, ObservedAt: at,
		},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateMFAPolicy(t, s); err != nil {
				t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
			}
		})
	}
}
