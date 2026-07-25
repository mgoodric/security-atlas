//go:build integration

// Integration tests for slice 036 — artifact store. Real Postgres + real
// MinIO. Skipped automatically when DATABASE_URL_APP or MINIO_ENDPOINT is
// unset (local dev without docker-compose minio + CI without the minio
// service container).

package artifact_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func minioEndpoint(t *testing.T) string {
	t.Helper()
	v := os.Getenv("MINIO_ENDPOINT")
	if v == "" {
		t.Skip("MINIO_ENDPOINT not set; skipping integration test")
	}
	return v
}

func minioBucket(t *testing.T) string {
	t.Helper()
	v := os.Getenv("MINIO_BUCKET")
	if v == "" {
		return "atlas-artifacts-test"
	}
	return v
}

// newS3Client builds an S3 client targeting MinIO with path-style
// addressing (MinIO requires this; virtual-host style would fail).
func newS3Client(t *testing.T, endpoint string) *s3.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	access := os.Getenv("MINIO_ACCESS_KEY")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_SECRET_KEY")
	if secret == "" {
		secret = "minioadmin"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

// ensureBucket makes the configured bucket exist before the tests run.
// MinIO returns BucketAlreadyOwnedByYou on re-create; we ignore that.
func ensureBucket(t *testing.T, cli *s3.Client, bucket string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") && !strings.Contains(err.Error(), "BucketAlreadyExists") {
		t.Fatalf("ensureBucket: %v", err)
	}
}

// freshTenant is kept inline (742 drain carve-out): it returns a uuid.UUID
// (the artifact tests pass the typed id to tenantCtx + assert on
// tenant.String()), a shape dbtest.SeedTenant — which returns a string —
// cannot express. Only its pool is re-routed to the dbtest harness.
func freshTenant(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM artifact_access_log WHERE tenant_id = $1`,
			`DELETE FROM artifacts WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func tenantCtx(t *testing.T, tenant uuid.UUID) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func buildStore(t *testing.T) (*artifact.Store, *pgxpool.Pool, *pgxpool.Pool, *s3.Client, string) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	endpoint := minioEndpoint(t)
	bucket := minioBucket(t)
	cli := newS3Client(t, endpoint)
	ensureBucket(t, cli, bucket)
	presigner := s3.NewPresignClient(cli)
	store, err := artifact.NewStore(artifact.Config{
		Pool:      app,
		S3:        cli,
		Presigner: &artifact.S3Presigner{Bucket: bucket, Client: presigner},
		Bucket:    bucket,
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, app, admin, cli, bucket
}

// ISC-11 + ISC-12 + AC-1 — basic upload writes to S3, persists metadata,
// re-hashes server-side.
func TestStore_PutAndGet_RoundTrip(t *testing.T) {
	store, _, admin, cli, bucket := buildStore(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	body := []byte("hello atlas " + uuid.NewString())
	wantHash := hashOf(body)

	art, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   int64(len(body)),
		ContentHash: wantHash,
		UploadedBy:  "key_test",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if art.ContentHash != wantHash {
		t.Fatalf("hash mismatch: got %q want %q", art.ContentHash, wantHash)
	}
	if art.SizeBytes != int64(len(body)) {
		t.Fatalf("size: got %d want %d", art.SizeBytes, len(body))
	}
	if got := art.StorageKey; got != "tenant-"+tenant.String()+"/"+art.ID.String() {
		t.Fatalf("storage_key shape: got %q", got)
	}

	// Verify the object actually landed in S3 at the expected key.
	headCtx, headCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer headCancel()
	out, err := cli.GetObject(headCtx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(art.StorageKey),
	})
	if err != nil {
		t.Fatalf("S3 GetObject: %v", err)
	}
	got, _ := io.ReadAll(out.Body)
	_ = out.Body.Close()
	if string(got) != string(body) {
		t.Fatalf("S3 body mismatch")
	}

	// Round-trip the metadata via Get.
	again, err := store.Get(ctx, art.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.ID != art.ID {
		t.Fatalf("Get id mismatch: %v vs %v", again.ID, art.ID)
	}
}

// ISC-12 / ISC-A4 — server re-hashes; a mismatching client-supplied hash
// is rejected.
func TestStore_Put_RejectsHashMismatch(t *testing.T) {
	store, _, admin, _, _ := buildStore(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	body := []byte("alpha")
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf([]byte("beta")), // mismatched
		UploadedBy:  "key_test",
		Body:        body,
	})
	if !errors.Is(err, artifact.ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

// ISC-14 + AC-3 + ISC-A1 — cross-tenant Get returns ErrNotFound, never
// reveals existence.
func TestStore_Get_CrossTenantReturnsNotFound(t *testing.T) {
	store, _, admin, _, _ := buildStore(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	body := []byte("tenant A's secret " + uuid.NewString())
	ctxA := tenantCtx(t, tenantA)
	art, err := store.Put(ctxA, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf(body),
		UploadedBy:  "key_a",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Put as tenant A: %v", err)
	}

	ctxB := tenantCtx(t, tenantB)
	_, err = store.Get(ctxB, art.ID)
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("cross-tenant Get must return ErrNotFound; got %v", err)
	}
}

// ISC-13 + ISC-A2 — Presign returns a URL with TTL ≤ MaxDownloadTTL.
func TestStore_Presign_TTLBound(t *testing.T) {
	store, _, admin, _, _ := buildStore(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	body := []byte("download me")
	art, err := store.Put(ctx, artifact.PutInput{
		ContentType: "application/octet-stream",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf(body),
		UploadedBy:  "key_test",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Request 24h — server must clamp to MaxDownloadTTL.
	pre, err := store.Presign(ctx, art.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("Presign: %v", err)
	}
	if pre.TTLSeconds > int64(artifact.MaxDownloadTTL/time.Second) {
		t.Fatalf("TTLSeconds %d exceeds MaxDownloadTTL %d", pre.TTLSeconds, int64(artifact.MaxDownloadTTL/time.Second))
	}
	if pre.URL == "" {
		t.Fatalf("Presign URL empty")
	}

	// Actually fetch the URL — proves the presign is valid against MinIO.
	httpCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(httpCtx, http.MethodGet, pre.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("download via presign: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(body) {
		t.Fatalf("presign download body mismatch: got %d bytes, status %d", len(got), resp.StatusCode)
	}
}

// ISC-22 + ISC-23 + ISC-A3 — every upload + every presign records an
// audit row.
func TestStore_LogAccess_AuditRowsPersist(t *testing.T) {
	store, _, admin, _, _ := buildStore(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	body := []byte("audited")
	art, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf(body),
		UploadedBy:  "key_test",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.LogAccess(ctx, art.ID, artifact.ActionUpload, "key_test"); err != nil {
		t.Fatalf("LogAccess upload: %v", err)
	}
	if _, err := store.Presign(ctx, art.ID, 0); err != nil {
		t.Fatalf("Presign: %v", err)
	}
	if err := store.LogAccess(ctx, art.ID, artifact.ActionDownload, "key_test"); err != nil {
		t.Fatalf("LogAccess download: %v", err)
	}

	// Query the audit log via the admin pool — atlas_migrate has
	// BYPASSRLS so the assertion is unambiguous about what landed.
	// (The atlas_app pool would also see the rows under the right
	// tenant GUC, but the admin pool keeps the assertion isolated
	// from the same transaction-set-config plumbing under test.)
	row, err := admin.Query(context.Background(), `
		SELECT count(*) FILTER (WHERE action = 'upload')::bigint,
		       count(*) FILTER (WHERE action = 'download')::bigint
		FROM artifact_access_log
		WHERE tenant_id = $1 AND artifact_id = $2
	`, tenant, art.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	defer row.Close()
	var uploads, downloads int64
	if !row.Next() {
		t.Fatalf("count row missing")
	}
	if err := row.Scan(&uploads, &downloads); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if uploads != 1 {
		t.Fatalf("upload audit rows = %d want 1", uploads)
	}
	if downloads != 1 {
		t.Fatalf("download audit rows = %d want 1", downloads)
	}
}

// ISC-18 / ErrOversized — over-cap body is rejected at the Store layer.
func TestStore_Put_RejectsOversized(t *testing.T) {
	store, _, admin, _, _ := buildStore(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	body := make([]byte, artifact.MaxUploadBytes+1)
	_, err := store.Put(ctx, artifact.PutInput{
		ContentType: "application/octet-stream",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf(body),
		UploadedBy:  "key_test",
		Body:        body,
	})
	if !errors.Is(err, artifact.ErrOversized) {
		t.Fatalf("expected ErrOversized, got %v", err)
	}
}

// ISC-6 — dedup: re-uploading identical bytes returns the same artifact
// id, no second S3 write attempt.
func TestStore_Put_DedupOnContentHash(t *testing.T) {
	store, _, admin, _, _ := buildStore(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	body := []byte(fmt.Sprintf("dedup-target-%s", uuid.NewString()))
	first, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf(body),
		UploadedBy:  "key_test",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	second, err := store.Put(ctx, artifact.PutInput{
		ContentType: "text/plain",
		SizeBytes:   int64(len(body)),
		ContentHash: hashOf(body),
		UploadedBy:  "key_test",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("dedup failed: ids differ %v vs %v", first.ID, second.ID)
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
