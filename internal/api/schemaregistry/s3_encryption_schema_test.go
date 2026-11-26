package schemaregistry

import (
	"context"
	"errors"
	"io/fs"
	"testing"
)

// #2815: aws.s3.bucket_encryption_state 1.1.0 adds kms_key_id + reason and
// must stay additive over 1.0.0, so 1.0.0 payloads keep validating.
func TestS3BucketEncryption110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	read := func(v string) []byte {
		t.Helper()
		b, err := fs.ReadFile(PlatformSchemasFS(), "aws.s3.bucket_encryption_state/"+v+".json")
		if err != nil {
			t.Fatalf("read %s: %v", v, err)
		}
		return b
	}
	if err := CheckAdditiveOver(read("1.0.0"), read("1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}

// NewEmbedded validates against the bundled schemas with no pool, and a
// lookup that would need the database reports ErrUnknownKind instead of
// dereferencing a nil pool.
func TestNewEmbedded_ValidatesWithoutDatabase(t *testing.T) {
	t.Parallel()
	s, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	ctx := context.Background()
	const tenant = "11111111-1111-1111-1111-111111111111"
	const kind = "aws.s3.bucket_encryption_state.v1"
	if err := s.ValidatePayload(ctx, tenant, kind, "1.1.0",
		[]byte(`{"bucket_name":"b","encryption_enabled":true,"sse_algorithm":"AES256"}`)); err != nil {
		t.Errorf("conforming payload rejected: %v", err)
	}
	if err := s.ValidatePayload(ctx, tenant, kind, "1.1.0",
		[]byte(`{"bucket_name":"b","encryption_enabled":true,"algorithm":"AES256"}`)); err == nil {
		t.Error("payload with an undeclared key accepted")
	}
	if err := s.ValidatePayload(ctx, tenant, "no.such.kind.v1", "1.0.0", []byte(`{}`)); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("unknown kind: err = %v; want ErrUnknownKind", err)
	}
	if !s.IsRegisteredForTenant(ctx, tenant, kind, "1.1.0") {
		t.Error("bundled 1.1.0 not registered")
	}
}
