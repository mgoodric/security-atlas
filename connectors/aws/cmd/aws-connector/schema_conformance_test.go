package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awsauth"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/awss3"
)

// validatePushed runs a record built by buildRecord through the check the
// DB-backed ingest stage applies to every push (internal/evidence/ingest:
// protojson-marshal the payload, then ValidatePayload against the
// registered schema for the record's kind and schema_version). The
// in-memory server the other tests push to only checks registration, so
// without this a payload/schema mismatch passes every unit test and is
// rejected in production (#2815).
func validatePushed(t *testing.T, state awss3.BucketState) error {
	t.Helper()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	identity := awsauth.Identity{AccountID: "111122223333", Environment: "prod", Source: "flag"}
	rec, err := buildRecord(state, identity, "scf:CRY-07")
	if err != nil {
		t.Fatalf("buildRecord: %v", err)
	}
	payloadJSON, err := protojson.Marshal(rec.GetPayload())
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return reg.ValidatePayload(context.Background(), tenantA, rec.GetEvidenceKind(), rec.GetSchemaVersion(), payloadJSON)
}

// TestBuildRecord_PayloadConformsToRegisteredSchema covers every result the
// connector emits: pass with SSE-S3, pass with SSE-KMS, fail (no default
// encryption) and inconclusive (an AWS error, carried in reason).
func TestBuildRecord_PayloadConformsToRegisteredSchema(t *testing.T) {
	t.Parallel()
	cases := map[string]awss3.BucketState{
		"pass_aes256": {
			BucketARN: "arn:aws:s3:::prod-vault", BucketName: "prod-vault", BucketRegion: "us-east-1",
			Result: awss3.ResultPass, Algorithm: "AES256",
		},
		"pass_kms": {
			BucketARN: "arn:aws:s3:::prod-logs", BucketName: "prod-logs", BucketRegion: "eu-west-1",
			Result: awss3.ResultPass, Algorithm: "aws:kms", KMSKeyID: "alias/logs", BucketKeyEnabled: true,
		},
		"fail_no_default_encryption": {
			BucketARN: "arn:aws:s3:::scratch", BucketName: "scratch", BucketRegion: "us-west-2",
			Result: awss3.ResultFail,
		},
		"inconclusive_region_unresolved": {
			BucketARN: "arn:aws:s3:::locked", BucketName: "locked",
			Result: awss3.ResultInconclusive, Reason: "resolve region: AccessDenied",
		},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validatePushed(t, state); err != nil {
				t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
			}
		})
	}
}

// TestLegacyPayload_RejectedBySchema pins the defect #2815 fixed: the key set
// the connector emitted before it (bucket_arn, algorithm, kms_key_id, ...)
// fails the registered schema, so ingest refused every such record.
func TestLegacyPayload_RejectedBySchema(t *testing.T) {
	t.Parallel()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	legacy := []byte(`{"bucket_arn":"arn:aws:s3:::prod-vault","bucket_name":"prod-vault",` +
		`"bucket_region":"us-east-1","algorithm":"AES256","kms_key_id":"",` +
		`"bucket_key_enabled":false,"reason":""}`)
	err = reg.ValidatePayload(context.Background(), tenantA, SupportedKind, "1.0.0", legacy)
	if err == nil || !strings.Contains(err.Error(), "rejected payload") {
		t.Fatalf("legacy payload: err = %v; want a schema rejection", err)
	}
}
