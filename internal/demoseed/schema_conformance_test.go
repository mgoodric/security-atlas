// schema_conformance_test.go — the demo evidence rows must be payloads the
// ingest stage would accept (#2863).
//
// writeEvidence inserts evidence_records directly rather than through
// ingest.Service (the #1830 exception), so the push-path schema validator
// never sees the demo payloads. These tests run every payload the seed
// writes through that same validator (schemaregistry.ValidatePayload over
// the embedded platform schemas, #2858) at the schema_version the row is
// stamped with. Pure Go, no Postgres (slice-353 Q-2 convention).
package demoseed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
)

func embeddedRegistry(t *testing.T) *schemaregistry.Service {
	t.Helper()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	return reg
}

func validateDemoPayload(t *testing.T, reg *schemaregistry.Service, kind, version string, payload map[string]any) error {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", kind, err)
	}
	return reg.ValidatePayload(context.Background(), "", kind, version, b)
}

// TestDemoEvidencePayloads_MatchRegisteredSchemas validates the evidence
// rows buildFixtures actually produces, exactly as writeEvidence will
// insert them: payload, kind and schema_version together.
func TestDemoEvidencePayloads_MatchRegisteredSchemas(t *testing.T) {
	t.Parallel()
	reg := embeddedRegistry(t)
	s := &Seeder{scale: 1.0, clock: time.Now}
	fs := s.buildFixtures("demo-schema")
	if len(fs.evidence) == 0 {
		t.Fatal("buildFixtures produced no evidence")
	}
	seen := map[string]bool{}
	for i, e := range fs.evidence {
		seen[e.EvidenceKind] = true
		if e.SchemaVer == "" {
			t.Errorf("evidence[%d] kind=%s has no schema_version", i, e.EvidenceKind)
			continue
		}
		if err := validateDemoPayload(t, reg, e.EvidenceKind, e.SchemaVer, e.Payload); err != nil {
			t.Errorf("evidence[%d] kind=%s result=%s: %v", i, e.EvidenceKind, e.Result, err)
		}
	}
	for _, kind := range evidenceKindsPool {
		if !seen[kind] {
			t.Errorf("kind %s is in evidenceKindsPool but no fixture row uses it", kind)
		}
	}
}

// TestBuildEvidencePayload_EveryKindAndResult covers every branch of
// buildEvidencePayload, including (kind, result) pairs the fixture cycle
// may not reach at scale 1.0.
func TestBuildEvidencePayload_EveryKindAndResult(t *testing.T) {
	t.Parallel()
	reg := embeddedRegistry(t)
	for _, kind := range evidenceKindsPool {
		version, ok := evidenceSchemaVersions[kind]
		if !ok {
			t.Errorf("kind %s has no entry in evidenceSchemaVersions", kind)
			continue
		}
		for _, result := range []string{"pass", "fail", "inconclusive", "na"} {
			for idx := 0; idx < 24; idx++ {
				if err := validateDemoPayload(t, reg, kind, version, buildEvidencePayload(kind, idx, result)); err != nil {
					t.Errorf("kind=%s result=%s idx=%d: %v", kind, result, idx, err)
				}
			}
		}
	}
}

// TestBuildEvidencePayload_S3AgreesWithResult pins the S3 payload to the
// row's verdict: a pass shows default encryption with an allowed
// algorithm (the fields the cc6_7 check reads, #2825); a fail or
// inconclusive row does not claim encryption.
func TestBuildEvidencePayload_S3AgreesWithResult(t *testing.T) {
	t.Parallel()
	const kind = "aws.s3.bucket_encryption_state.v1"
	for idx := 0; idx < 4; idx++ {
		pass := buildEvidencePayload(kind, idx, "pass")
		if pass["encryption_enabled"] != true {
			t.Errorf("idx=%d pass: encryption_enabled = %v; want true", idx, pass["encryption_enabled"])
		}
		if alg := pass["sse_algorithm"]; alg != "AES256" && alg != "aws:kms" {
			t.Errorf("idx=%d pass: sse_algorithm = %v; want AES256 or aws:kms", idx, alg)
		}
		for _, result := range []string{"fail", "inconclusive"} {
			p := buildEvidencePayload(kind, idx, result)
			if p["encryption_enabled"] != false {
				t.Errorf("idx=%d %s: encryption_enabled = %v; want false", idx, result, p["encryption_enabled"])
			}
			if _, ok := p["sse_algorithm"]; ok {
				t.Errorf("idx=%d %s: sse_algorithm present; want absent", idx, result)
			}
		}
	}
	// The seed's fixture rows must show both allowed algorithms.
	s := &Seeder{scale: 1.0, clock: time.Now}
	algs := map[any]bool{}
	for _, e := range s.buildFixtures("demo-s3-algs").evidence {
		if e.EvidenceKind == kind && e.Payload["encryption_enabled"] == true {
			algs[e.Payload["sse_algorithm"]] = true
		}
	}
	if !algs["AES256"] || !algs["aws:kms"] {
		t.Errorf("seeded S3 pass rows use algorithms %v; want both AES256 and aws:kms", algs)
	}
	if r := buildEvidencePayload(kind, 0, "inconclusive")["reason"]; r == nil || r == "" {
		t.Error("inconclusive S3 payload has no reason")
	}
}
