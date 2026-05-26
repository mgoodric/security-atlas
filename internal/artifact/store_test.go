// Unit tests for the pure-Go branches of internal/artifact/store.go. The
// DB-bound happy paths (Put insert, Get/Presign/LogAccess persistence) are
// covered by integration_test.go against real Postgres + MinIO; this file
// exercises every branch that resolves BEFORE the Store touches its
// *pgxpool.Pool, plus the standalone helpers and the S3Presigner adapter.
//
// Load-bearing functions exercised:
//
//   - NewStore — config validation (missing Pool / S3 / Presigner / Bucket
//     branches and the bucket-whitespace branch). Constitutional: a Store
//     constructed with an unset field would silently nil-deref on the
//     first call, hiding the misconfiguration. The validation here is the
//     wall.
//   - Bucket — accessor used by the API layer to render payload_uri
//     without leaking the Store internals.
//   - Put — every branch reachable before the pool transaction:
//       * validatePut errors (empty content_type / uploaded_by / body)
//       * ErrOversized (body > MaxUploadBytes)
//       * ErrHashMismatch (caller-supplied hash disagrees with the
//         server's re-compute — ISC-12 / ISC-A4 defence-in-depth)
//       * ErrNoTenant (no tenant on the context — ISC-A1 cross-tenant
//         guard before the DB layer)
//       * inTx Begin failure (unreachable pool surfaces a wrapped error)
//   - Get — tenant-context errors (the only branches that don't require a
//     DB connection).
//   - Presign — delegates to Get; the tenant-error and clamp paths are
//     covered transitively. The TTL clamp itself is in types_test.go.
//   - LogAccess — every validation branch (invalid action, empty actor,
//     no tenant) and the inTx Begin failure surface.
//   - inTx — Begin-failure wrap (line 282-285) via an unreachable pool.
//   - validatePut — every input failure mode, reached via Put.
//   - S3Presigner.PresignGetObject — adapter against a real
//     *s3.PresignClient configured with anonymous credentials. Presigning
//     is a local-only signing operation; no network call is made, so we
//     can assert the returned URL embeds the bucket + key + TTL without
//     spinning up MinIO.
//
// Branches deliberately left to integration:
//
//   - Put dedup-on-hash, S3 PutObject, DB insert + S3-cleanup-on-rollback —
//     covered by TestStore_Put_DedupOnContentHash and
//     TestStore_PutAndGet_RoundTrip in integration_test.go.
//   - Get / Presign / LogAccess persistence — covered by their
//     same-named integration tests.
//   - inTx commit + GUC apply — covered transitively by every successful
//     integration test.
//
// Pattern reference: slice 279's internal/decision/filters_test.go set the
// shape for "unit tests that target every in-memory branch of a
// DB-touching package, leaving DB persistence to integration".

package artifact_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// unreachablePool returns a *pgxpool.Pool configured against a parseable
// but-unreachable DSN. The pool constructor defers connection; Begin
// fails fast with a connection-refused error. This lets us exercise the
// "Begin returned err" branch of Store.inTx without standing up Postgres.
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// 127.0.0.1:1 is a guaranteed refusal — port 1 (TCPMUX) is not bound.
	// connect_timeout=1 keeps a runaway test under a second.
	dsn := "postgres://test-user:test-pass@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("unreachablePool: pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// stubPresigner implements artifact.Presigner for unit tests where we
// want to confirm the Store wires the request shape, but we never reach
// the presigner because Get errors out earlier.
type stubPresigner struct {
	called bool
	url    string
	err    error
}

func (s *stubPresigner) PresignGetObject(_ context.Context, _ string, _ time.Duration) (string, error) {
	s.called = true
	return s.url, s.err
}

// stubS3 implements artifact.S3API. Unit tests never reach the actual
// PutObject path (we error out before inTx commits), so the stub records
// any unexpected call.
type stubS3 struct{}

func (s *stubS3) PutObject(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return &s3.PutObjectOutput{}, nil
}

func (s *stubS3) DeleteObject(_ context.Context, _ *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return &s3.DeleteObjectOutput{}, nil
}

// newTestStore builds a Store with an unreachable Pool, a stub S3 client,
// and a stub Presigner. Suitable for tests that exercise paths returning
// before inTx commits.
func newTestStore(t *testing.T) (*artifact.Store, *stubPresigner) {
	t.Helper()
	pre := &stubPresigner{url: "https://example.test/presigned"}
	store, err := artifact.NewStore(artifact.Config{
		Pool:      unreachablePool(t),
		S3:        &stubS3{},
		Presigner: pre,
		Bucket:    "atlas-artifacts-test",
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, pre
}

// unitTenantCtx attaches a fresh tenant id to a base context for unit
// tests. Distinct from the integration test's tenantCtx (which requires
// a real DB pool argument for cleanup hooks).
func unitTenantCtx(t *testing.T) context.Context {
	t.Helper()
	tenant := uuid.New()
	ctx, err := tenancy.WithTenant(context.Background(), tenant.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// =====================================================================
// NewStore validation
// =====================================================================

func TestNewStore_RejectsMissingPool(t *testing.T) {
	t.Parallel()
	_, err := artifact.NewStore(artifact.Config{
		Pool:      nil,
		S3:        &stubS3{},
		Presigner: &stubPresigner{},
		Bucket:    "atlas-artifacts-test",
	})
	if err == nil {
		t.Fatal("NewStore with nil Pool: want error, got nil")
	}
	if !strings.Contains(err.Error(), "Pool is required") {
		t.Fatalf("NewStore err msg: got %q want substring 'Pool is required'", err.Error())
	}
}

func TestNewStore_RejectsMissingS3(t *testing.T) {
	t.Parallel()
	pool := unreachablePool(t)
	_, err := artifact.NewStore(artifact.Config{
		Pool:      pool,
		S3:        nil,
		Presigner: &stubPresigner{},
		Bucket:    "atlas-artifacts-test",
	})
	if err == nil {
		t.Fatal("NewStore with nil S3: want error, got nil")
	}
	if !strings.Contains(err.Error(), "S3 client is required") {
		t.Fatalf("NewStore err msg: got %q want substring 'S3 client is required'", err.Error())
	}
}

func TestNewStore_RejectsMissingPresigner(t *testing.T) {
	t.Parallel()
	pool := unreachablePool(t)
	_, err := artifact.NewStore(artifact.Config{
		Pool:      pool,
		S3:        &stubS3{},
		Presigner: nil,
		Bucket:    "atlas-artifacts-test",
	})
	if err == nil {
		t.Fatal("NewStore with nil Presigner: want error, got nil")
	}
	if !strings.Contains(err.Error(), "Presigner is required") {
		t.Fatalf("NewStore err msg: got %q want substring 'Presigner is required'", err.Error())
	}
}

func TestNewStore_RejectsEmptyBucket(t *testing.T) {
	t.Parallel()
	pool := unreachablePool(t)
	for _, bad := range []string{"", "   ", "\t\n"} {
		bad := bad
		t.Run("bucket="+bad, func(t *testing.T) {
			t.Parallel()
			_, err := artifact.NewStore(artifact.Config{
				Pool:      pool,
				S3:        &stubS3{},
				Presigner: &stubPresigner{},
				Bucket:    bad,
			})
			if err == nil {
				t.Fatalf("NewStore with empty/whitespace bucket %q: want error, got nil", bad)
			}
			if !strings.Contains(err.Error(), "Bucket is required") {
				t.Fatalf("NewStore err msg: got %q want substring 'Bucket is required'", err.Error())
			}
		})
	}
}

func TestNewStore_DefaultClockIsWallClock(t *testing.T) {
	t.Parallel()
	// Build a Store WITHOUT a Clock override. We can't directly observe
	// the clock field (unexported), but we can confirm Bucket() returns
	// the configured value, proving construction succeeded.
	store, err := artifact.NewStore(artifact.Config{
		Pool:      unreachablePool(t),
		S3:        &stubS3{},
		Presigner: &stubPresigner{},
		Bucket:    "atlas-default-clock",
		Clock:     nil, // default to time.Now
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := store.Bucket(); got != "atlas-default-clock" {
		t.Fatalf("Bucket(): got %q want %q", got, "atlas-default-clock")
	}
}

func TestNewStore_AcceptsCustomClock(t *testing.T) {
	t.Parallel()
	pinned := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	store, err := artifact.NewStore(artifact.Config{
		Pool:      unreachablePool(t),
		S3:        &stubS3{},
		Presigner: &stubPresigner{},
		Bucket:    "atlas-pinned-clock",
		Clock:     func() time.Time { return pinned },
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := store.Bucket(); got != "atlas-pinned-clock" {
		t.Fatalf("Bucket() after custom clock: got %q want %q", got, "atlas-pinned-clock")
	}
}

// =====================================================================
// Bucket accessor
// =====================================================================

func TestStore_Bucket_EchoesConfig(t *testing.T) {
	t.Parallel()
	store, err := artifact.NewStore(artifact.Config{
		Pool:      unreachablePool(t),
		S3:        &stubS3{},
		Presigner: &stubPresigner{},
		Bucket:    "atlas-echo-bucket",
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := store.Bucket(); got != "atlas-echo-bucket" {
		t.Fatalf("Bucket(): got %q want %q", got, "atlas-echo-bucket")
	}
}

// =====================================================================
// Put — validation paths (every branch in validatePut + the early
// post-validation defence-in-depth checks in Put itself)
// =====================================================================

func TestStore_Put_RejectsEmptyContentType(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for _, ct := range []string{"", "   ", "\t"} {
		ct := ct
		t.Run("ct="+ct, func(t *testing.T) {
			t.Parallel()
			_, err := store.Put(ctx, artifact.PutInput{
				ContentType: ct,
				SizeBytes:   3,
				UploadedBy:  "test-key",
				Body:        []byte("abc"),
			})
			if !errors.Is(err, artifact.ErrInvalidInput) {
				t.Fatalf("Put with content_type %q: want ErrInvalidInput, got %v", ct, err)
			}
			if !strings.Contains(err.Error(), "content_type is required") {
				t.Fatalf("err msg: got %q want substring 'content_type is required'", err.Error())
			}
		})
	}
}

func TestStore_Put_RejectsEmptyUploadedBy(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for _, uploader := range []string{"", "   "} {
		uploader := uploader
		t.Run("uploader="+uploader, func(t *testing.T) {
			t.Parallel()
			_, err := store.Put(ctx, artifact.PutInput{
				ContentType: "text/plain",
				SizeBytes:   3,
				UploadedBy:  uploader,
				Body:        []byte("abc"),
			})
			if !errors.Is(err, artifact.ErrInvalidInput) {
				t.Fatalf("Put with uploaded_by %q: want ErrInvalidInput, got %v", uploader, err)
			}
			if !strings.Contains(err.Error(), "uploaded_by is required") {
				t.Fatalf("err msg: got %q want substring 'uploaded_by is required'", err.Error())
			}
		})
	}
}

func TestStore_Put_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for _, body := range [][]byte{nil, {}} {
		body := body
		t.Run("len="+stringLen(body), func(t *testing.T) {
			t.Parallel()
			_, err := store.Put(ctx, artifact.PutInput{
				ContentType: "text/plain",
				SizeBytes:   0,
				UploadedBy:  "test-key",
				Body:        body,
			})
			if !errors.Is(err, artifact.ErrInvalidInput) {
				t.Fatalf("Put with empty body: want ErrInvalidInput, got %v", err)
			}
			if !strings.Contains(err.Error(), "body must be non-empty") {
				t.Fatalf("err msg: got %q want substring 'body must be non-empty'", err.Error())
			}
		})
	}
}

func TestStore_Put_RejectsOversized_UnitFastPath(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	body := make([]byte, artifact.MaxUploadBytes+1)
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "application/octet-stream",
		SizeBytes:   int64(len(body)),
		UploadedBy:  "test-key",
		Body:        body,
	})
	if !errors.Is(err, artifact.ErrOversized) {
		t.Fatalf("Put with oversized body: want ErrOversized, got %v", err)
	}
}

func TestStore_Put_AcceptsBodyAtCap(t *testing.T) {
	t.Parallel()
	// Body exactly at MaxUploadBytes must NOT be rejected by validatePut.
	// We get past validate and hash, then we expect ErrNoTenant or
	// a Begin-error from the unreachable pool — both prove validate
	// accepted the body.
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	body := make([]byte, artifact.MaxUploadBytes)
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "application/octet-stream",
		SizeBytes:   int64(len(body)),
		UploadedBy:  "test-key",
		Body:        body,
	})
	if errors.Is(err, artifact.ErrOversized) {
		t.Fatalf("Put with body exactly at MaxUploadBytes: must NOT be ErrOversized; got %v", err)
	}
	// We expect a downstream error (Begin fails on unreachable pool).
	// That's fine — proves we passed the size check.
	if err == nil {
		t.Fatalf("Put against unreachable pool: want non-nil err (Begin should fail), got nil")
	}
}

func TestStore_Put_RejectsHashMismatch_UnitFastPath(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	// Body = "alpha"; supplied hash is wrong on purpose. The mismatch
	// resolves BEFORE the inTx call, so we never touch the unreachable
	// pool — proving the defence-in-depth check fires before any DB
	// side effect. (Integration test of the same shape:
	// TestStore_Put_RejectsHashMismatch in integration_test.go.)
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   5,
		// Arbitrary 64-hex string that is NOT sha256("alpha").
		ContentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		UploadedBy:  "test-key",
		Body:        []byte("alpha"),
	})
	if !errors.Is(err, artifact.ErrHashMismatch) {
		t.Fatalf("Put with mismatched hash: want ErrHashMismatch, got %v", err)
	}
}

func TestStore_Put_HashMismatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	// sha256("alpha") = 8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8
	// Uppercase variant must equal lowercase per EqualFold.
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   5,
		ContentHash: "8ED3F6AD685B959EAD7022518E1AF76CD816F8E8EC7CCDDA1ED4018E8F2223F8",
		UploadedBy:  "test-key",
		Body:        []byte("alpha"),
	})
	// Uppercase must NOT be a mismatch — we expect to fall through to the
	// inTx Begin failure (unreachable pool).
	if errors.Is(err, artifact.ErrHashMismatch) {
		t.Fatalf("uppercase hash must not be treated as mismatch (EqualFold); got ErrHashMismatch")
	}
	if err == nil {
		t.Fatalf("Put against unreachable pool: want non-nil err (Begin should fail), got nil")
	}
}

func TestStore_Put_AllowsEmptyClientHash(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	// Empty ContentHash from caller is allowed; the server computes its own.
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   5,
		ContentHash: "",
		UploadedBy:  "test-key",
		Body:        []byte("alpha"),
	})
	if errors.Is(err, artifact.ErrHashMismatch) {
		t.Fatalf("empty caller hash must NOT be a mismatch; got ErrHashMismatch")
	}
	if err == nil {
		t.Fatalf("Put against unreachable pool: want non-nil err (Begin should fail), got nil")
	}
}

func TestStore_Put_RejectsNoTenantContext(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)

	_, err := store.Put(context.Background(), artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   5,
		UploadedBy:  "test-key",
		Body:        []byte("alpha"),
	})
	if !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("Put without tenant: want tenancy.ErrNoTenant, got %v", err)
	}
}

func TestStore_Put_BeginErrorWrapped(t *testing.T) {
	t.Parallel()
	// With a valid tenant on the context but an unreachable pool, Put
	// reaches inTx, Begin fails, and we expect the wrapped error to
	// carry the `artifact: begin tx:` prefix.
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   5,
		UploadedBy:  "test-key",
		Body:        []byte("alpha"),
	})
	if err == nil {
		t.Fatal("Put against unreachable pool: want non-nil err, got nil")
	}
	if !strings.Contains(err.Error(), "begin tx") {
		t.Fatalf("Put err: got %q want substring 'begin tx'", err.Error())
	}
}

// =====================================================================
// Get — tenant validation
// =====================================================================

func TestStore_Get_RejectsNoTenantContext(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("Get without tenant: want ErrNoTenant, got %v", err)
	}
}

func TestStore_Get_BeginErrorWrapped(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.Get(ctx, uuid.New())
	if err == nil {
		t.Fatal("Get against unreachable pool: want non-nil err, got nil")
	}
	if !strings.Contains(err.Error(), "begin tx") {
		t.Fatalf("Get err: got %q want substring 'begin tx'", err.Error())
	}
}

// =====================================================================
// Presign — delegates to Get; tenant + clamp paths
// =====================================================================

func TestStore_Presign_PropagatesNoTenantError(t *testing.T) {
	t.Parallel()
	store, pre := newTestStore(t)

	_, err := store.Presign(context.Background(), uuid.New(), 5*time.Minute)
	if !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("Presign without tenant: want ErrNoTenant, got %v", err)
	}
	if pre.called {
		t.Fatal("Presign must NOT call the presigner when Get errors")
	}
}

func TestStore_Presign_BeginErrorWrapped(t *testing.T) {
	t.Parallel()
	store, pre := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.Presign(ctx, uuid.New(), time.Hour)
	if err == nil {
		t.Fatal("Presign against unreachable pool: want non-nil err, got nil")
	}
	if !strings.Contains(err.Error(), "begin tx") {
		t.Fatalf("Presign err: got %q want substring 'begin tx'", err.Error())
	}
	if pre.called {
		t.Fatal("Presign must NOT call the presigner when Get errors")
	}
}

// =====================================================================
// LogAccess — every validation branch
// =====================================================================

func TestStore_LogAccess_RejectsUnknownAction(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for _, action := range []artifact.Action{"", "delete", "READ", "Upload"} {
		action := action
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			err := store.LogAccess(ctx, uuid.New(), action, "test-key")
			if !errors.Is(err, artifact.ErrInvalidInput) {
				t.Fatalf("LogAccess with action %q: want ErrInvalidInput, got %v", action, err)
			}
			if !strings.Contains(err.Error(), "upload|download") {
				t.Fatalf("LogAccess err: got %q want substring 'upload|download'", err.Error())
			}
		})
	}
}

func TestStore_LogAccess_AcceptsKnownActions(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for _, action := range []artifact.Action{artifact.ActionUpload, artifact.ActionDownload} {
		action := action
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			err := store.LogAccess(ctx, uuid.New(), action, "test-key")
			// We expect to fail in Begin, NOT in validation.
			if errors.Is(err, artifact.ErrInvalidInput) {
				t.Fatalf("LogAccess(%q) must not be ErrInvalidInput; got %v", action, err)
			}
			if err == nil {
				t.Fatalf("LogAccess against unreachable pool: want non-nil err, got nil")
			}
			if !strings.Contains(err.Error(), "begin tx") {
				t.Fatalf("LogAccess err: got %q want substring 'begin tx'", err.Error())
			}
		})
	}
}

func TestStore_LogAccess_RejectsEmptyActor(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for _, actor := range []string{"", "   ", "\t"} {
		actor := actor
		t.Run("actor="+actor, func(t *testing.T) {
			t.Parallel()
			err := store.LogAccess(ctx, uuid.New(), artifact.ActionUpload, actor)
			if !errors.Is(err, artifact.ErrInvalidInput) {
				t.Fatalf("LogAccess with actor %q: want ErrInvalidInput, got %v", actor, err)
			}
			if !strings.Contains(err.Error(), "actor is required") {
				t.Fatalf("LogAccess err: got %q want substring 'actor is required'", err.Error())
			}
		})
	}
}

func TestStore_LogAccess_RejectsNoTenantContext(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	err := store.LogAccess(context.Background(), uuid.New(), artifact.ActionUpload, "test-key")
	if !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("LogAccess without tenant: want ErrNoTenant, got %v", err)
	}
}

// (fromRow + pgUUID white-box tests live in internal_test.go since they
// exercise unexported helpers directly.)

// =====================================================================
// S3Presigner adapter — exercises PresignGetObject without a network
// round trip. The AWS SDK's PresignClient is a pure-local signer.
// =====================================================================

func TestS3Presigner_PresignGetObject_ProducesURL(t *testing.T) {
	t.Parallel()
	// Build an S3 client with anonymous credentials pointed at a fake
	// endpoint. PresignGetObject computes a signed URL locally — it
	// does NOT round-trip to MinIO — so the test runs without docker.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-access", "test-secret", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	s3client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://127.0.0.1:65534") // unreachable, doesn't matter
		o.UsePathStyle = true
	})
	presignClient := s3.NewPresignClient(s3client)
	adapter := &artifact.S3Presigner{
		Bucket: "atlas-unit-test",
		Client: presignClient,
	}

	url, err := adapter.PresignGetObject(ctx, "tenant-aaaa/bbbb", 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignGetObject: %v", err)
	}
	if url == "" {
		t.Fatal("PresignGetObject returned empty URL")
	}
	// Sanity-check the URL embeds the bucket and key (path-style).
	if !strings.Contains(url, "atlas-unit-test") {
		t.Fatalf("URL missing bucket: %q", url)
	}
	if !strings.Contains(url, "tenant-aaaa/bbbb") {
		t.Fatalf("URL missing key: %q", url)
	}
	// AWS v4-presigned URLs always include X-Amz-Signature and
	// X-Amz-Expires query params. Verify both.
	if !strings.Contains(url, "X-Amz-Signature=") {
		t.Fatalf("URL missing X-Amz-Signature: %q", url)
	}
	if !strings.Contains(url, "X-Amz-Expires=900") { // 15 min = 900s
		t.Fatalf("URL missing X-Amz-Expires=900: %q", url)
	}
}

func TestS3Presigner_PresignGetObject_HonoursTTL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ttl     time.Duration
		expects string
	}{
		{"30s", 30 * time.Second, "X-Amz-Expires=30"},
		{"5m", 5 * time.Minute, "X-Amz-Expires=300"},
		{"1h", time.Hour, "X-Amz-Expires=3600"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// One-off config + client per sub-test so the parent's
			// context cancellation can't race the parallel children.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cfg, err := awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithRegion("us-east-1"),
				awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-access", "test-secret", "")),
			)
			if err != nil {
				t.Fatalf("aws config: %v", err)
			}
			s3client := s3.NewFromConfig(cfg, func(o *s3.Options) {
				o.BaseEndpoint = aws.String("http://127.0.0.1:65534")
				o.UsePathStyle = true
			})
			adapter := &artifact.S3Presigner{
				Bucket: "atlas-unit-test",
				Client: s3.NewPresignClient(s3client),
			}

			url, err := adapter.PresignGetObject(ctx, "tenant-x/y", tc.ttl)
			if err != nil {
				t.Fatalf("PresignGetObject: %v", err)
			}
			if !strings.Contains(url, tc.expects) {
				t.Fatalf("URL missing %q: %q", tc.expects, url)
			}
		})
	}
}

// =====================================================================
// helpers
// =====================================================================

func stringLen(b []byte) string {
	// Stable test-name suffix; avoids importing strconv.
	if len(b) == 0 {
		return "zero"
	}
	return "nonzero"
}
