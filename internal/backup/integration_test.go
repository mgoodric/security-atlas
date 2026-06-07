//go:build integration

// Integration tests for slice 510 — automated backup + restore-verification.
// Real Postgres (DATABASE_URL = BYPASSRLS migrator, DATABASE_URL_APP =
// RLS-enforced app role) + real MinIO. Skipped automatically when the env is
// unset (local dev without docker-compose).
//
// Run with: go test -tags=integration -p 1 ./internal/backup/...
//
// Covers the load-bearing ACs:
//   AC-2  local + S3 targets (MinIO).
//   AC-3  retention/rotation (no unbounded growth).
//   AC-4  full backup -> restore -> verify cycle + ephemeral-DB teardown.
//   AC-5  corrupted artifact fails verification.
//   AC-6  status rows for every run.
//   AC-7  atlas_app (tenant role) cannot read backup_runs.

package backup_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/backup"
)

func migratorDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newMinioClient(t *testing.T) (*s3.Client, string) {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_ENDPOINT not set; skipping S3 leg")
	}
	access := os.Getenv("MINIO_ACCESS_KEY")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_SECRET_KEY")
	if secret == "" {
		secret = "minioadmin"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	bucket := "atlas-backups-test"
	_, err = cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") && !strings.Contains(err.Error(), "BucketAlreadyExists") {
		t.Fatalf("create bucket: %v", err)
	}
	return cli, bucket
}

func cleanupBackupRuns(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		// backup_runs has no DELETE grant for atlas_app; the migrator pool
		// can DELETE (no policy blocks the BYPASSRLS role). Best-effort.
		_, _ = pool.Exec(context.Background(), `DELETE FROM backup_runs`)
	})
}

// --- AC-4: full backup -> restore -> verify cycle (local target) ---

func TestBackupRestoreVerifyCycle_Local(t *testing.T) {
	migDSN := migratorDSN(t)
	pool := openPool(t, migDSN)
	cleanupBackupRuns(t, pool)
	ctx := context.Background()

	// Seed a row with BOTH an array (text[]) and a jsonb column so the
	// dump→replay cycle exercises both — pgx decodes a text[] and a jsonb to
	// []any, so this guards the array-vs-jsonb literal disambiguation that a
	// live boot once tripped on.
	seedKind := "backup.testkind." + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM evidence_kind_schemas WHERE kind = $1`, seedKind)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO evidence_kind_schemas
		  (id, kind, semver, major, minor, patch, schema_json, owner, default_scf_anchors, created_by)
		VALUES (gen_random_uuid(), $1, '1.0.0', 1, 0, 0, '{"type":"object"}'::jsonb, 'tester',
		        ARRAY['SCF:IAC-01','SCF:IAC-02']::text[], 'backup-test')`, seedKind); err != nil {
		t.Fatalf("seed array+jsonb row: %v", err)
	}

	dir := t.TempDir()
	target, err := backup.NewLocalTarget(dir)
	if err != nil {
		t.Fatalf("NewLocalTarget: %v", err)
	}
	cfg := backup.Config{TargetKind: "local", Dir: dir, KeepDaily: 7, KeepWeekly: 4, MaintenanceDB: "postgres"}

	bk := backup.NewBackuper(pool, target, cfg, nil)
	res, err := bk.BackupOnce(ctx)
	if err != nil {
		t.Fatalf("BackupOnce: %v", err)
	}
	if res.Outcome != "succeeded" || res.Size == 0 || res.Hash == "" {
		t.Fatalf("backup result unexpected: %+v", res)
	}

	// AC-6: a succeeded backup status row exists.
	assertLatestRun(t, pool, "backup", "succeeded")

	// AC-4: verify the latest backup — restore into ephemeral DB + smoke.
	vf := backup.NewVerifier(pool, target, migDSN, "postgres", nil)
	vres, err := vf.VerifyOnce(ctx)
	if err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}
	if vres.Outcome != "succeeded" || vres.Tables == 0 {
		t.Fatalf("verify result unexpected: %+v", vres)
	}
	assertLatestRun(t, pool, "verify", "succeeded")

	// P0-510-2: no ephemeral DB left standing.
	assertNoEphemeralDBs(t, ctx, migDSN)
}

// --- AC-2: S3 (MinIO) target full cycle ---

func TestBackupRestoreVerifyCycle_S3(t *testing.T) {
	migDSN := migratorDSN(t)
	pool := openPool(t, migDSN)
	cleanupBackupRuns(t, pool)
	cli, bucket := newMinioClient(t)
	ctx := context.Background()

	prefix := "verify-" + uuid.NewString()[:8]
	target, err := backup.NewS3Target(cli, bucket, prefix)
	if err != nil {
		t.Fatalf("NewS3Target: %v", err)
	}
	cfg := backup.Config{TargetKind: "s3", S3Bucket: bucket, S3Prefix: prefix, KeepDaily: 7, KeepWeekly: 4, MaintenanceDB: "postgres"}

	bk := backup.NewBackuper(pool, target, cfg, nil)
	if _, err := bk.BackupOnce(ctx); err != nil {
		t.Fatalf("BackupOnce(s3): %v", err)
	}
	// Round-trip via List + Get proves AC-2 S3 store works.
	objs, err := target.List(ctx)
	if err != nil || len(objs) == 0 {
		t.Fatalf("S3 List: %v objs=%d", err, len(objs))
	}

	vf := backup.NewVerifier(pool, target, migDSN, "postgres", nil)
	vres, err := vf.VerifyOnce(ctx)
	if err != nil {
		t.Fatalf("VerifyOnce(s3): %v", err)
	}
	if vres.Outcome != "succeeded" {
		t.Fatalf("s3 verify outcome = %s", vres.Outcome)
	}
	assertNoEphemeralDBs(t, ctx, migDSN)
}

// --- AC-5: corrupted artifact fails verification loudly ---

func TestVerifyFailsOnCorruptedArtifact(t *testing.T) {
	migDSN := migratorDSN(t)
	pool := openPool(t, migDSN)
	cleanupBackupRuns(t, pool)
	ctx := context.Background()

	dir := t.TempDir()
	target, err := backup.NewLocalTarget(dir)
	if err != nil {
		t.Fatalf("NewLocalTarget: %v", err)
	}
	cfg := backup.Config{TargetKind: "local", Dir: dir, KeepDaily: 7, KeepWeekly: 4, MaintenanceDB: "postgres"}

	bk := backup.NewBackuper(pool, target, cfg, nil)
	res, err := bk.BackupOnce(ctx)
	if err != nil {
		t.Fatalf("BackupOnce: %v", err)
	}

	// Tamper with the artifact bytes WITHOUT updating the recorded hash.
	if err := os.WriteFile(dir+"/"+res.Name, []byte("CORRUPTED -- not the real dump\n"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	vf := backup.NewVerifier(pool, target, migDSN, "postgres", nil)
	vres, verr := vf.VerifyOnce(ctx)
	if verr == nil {
		t.Fatal("VerifyOnce on a corrupted artifact must return an error")
	}
	if !strings.Contains(verr.Error(), "hash mismatch") {
		t.Fatalf("expected hash-mismatch failure, got: %v", verr)
	}
	if vres.Outcome != "failed" {
		t.Fatalf("verify outcome = %s, want failed", vres.Outcome)
	}
	// The tampered dump must NEVER have been replayed -> no ephemeral DB.
	assertNoEphemeralDBs(t, ctx, migDSN)
}

// --- AC-3: retention/rotation bounds growth ---

func TestRotationBoundsGrowth(t *testing.T) {
	dir := t.TempDir()
	target, err := backup.NewLocalTarget(dir)
	if err != nil {
		t.Fatalf("NewLocalTarget: %v", err)
	}
	ctx := context.Background()
	// Seed 10 dated backup artifacts across 10 days.
	base := time.Date(2026, 6, 7, 2, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		name := backup.ArtifactName(base.AddDate(0, 0, -i))
		if err := target.Put(ctx, name, []byte(fmt.Sprintf("dump %d", i))); err != nil {
			t.Fatalf("seed put: %v", err)
		}
	}
	objs, _ := target.List(ctx)
	if len(objs) != 10 {
		t.Fatalf("seeded %d, want 10", len(objs))
	}
	// Apply rotation with a small window: keep 2 daily + 1 weekly.
	for _, dead := range backup.SelectForDeletion(objs, 2, 1) {
		if err := target.Delete(ctx, dead); err != nil {
			t.Fatalf("delete %s: %v", dead, err)
		}
	}
	after, _ := target.List(ctx)
	if len(after) >= 10 {
		t.Fatalf("rotation did not bound growth: %d remain", len(after))
	}
	if len(after) == 0 {
		t.Fatal("rotation deleted everything; window must retain some")
	}
}

// --- AC-7 / P0-510-1: atlas_app (tenant role) cannot read backup_runs ---

func TestTenantRoleCannotReadBackupRuns(t *testing.T) {
	appPool := openPool(t, appDSN(t))
	ctx := context.Background()
	_, err := appPool.Exec(ctx, `SELECT 1 FROM backup_runs LIMIT 1`)
	if err == nil {
		t.Fatal("atlas_app could read backup_runs; P0-510-1 requires the grant to be absent")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("expected permission-denied for atlas_app on backup_runs, got: %v", err)
	}
	// And it cannot INSERT either.
	_, ierr := appPool.Exec(ctx, `INSERT INTO backup_runs (kind) VALUES ('backup')`)
	if ierr == nil || !strings.Contains(strings.ToLower(ierr.Error()), "permission denied") {
		t.Fatalf("atlas_app must be denied INSERT on backup_runs, got: %v", ierr)
	}
}

// --- AC-6 / D9: a failure raises an in-app notification (composes w/ 445) ---

func TestFailureRaisesNotification(t *testing.T) {
	migDSN := migratorDSN(t)
	pool := openPool(t, migDSN)
	cleanupBackupRuns(t, pool)
	ctx := context.Background()

	tenantID := uuid.NewString()
	recipient := "backup-admin-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM notifications WHERE tenant_id = $1`, tenantID)
	})

	dir := t.TempDir()
	target, err := backup.NewLocalTarget(dir)
	if err != nil {
		t.Fatalf("NewLocalTarget: %v", err)
	}

	// Verify with NO backup present forces a failure ("no backup to verify").
	vf := backup.NewVerifier(pool, target, migDSN, "postgres", nil)
	vf.SetAlertHook(backup.NewNotificationAlerter(pool, tenantID, recipient, nil))
	if _, verr := vf.VerifyOnce(ctx); verr == nil {
		t.Fatal("expected verify to fail with no backup present")
	}

	// A backup.failure notification must have been written for the recipient.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE tenant_id = $1 AND recipient_user_id = $2 AND type = $3`,
		tenantID, recipient, backup.NotificationType).Scan(&n); err != nil {
		t.Fatalf("notification count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 backup.failure notification, got %d", n)
	}
}

// --- helpers ---

func assertLatestRun(t *testing.T, pool *pgxpool.Pool, kind, wantOutcome string) {
	t.Helper()
	var outcome string
	err := pool.QueryRow(context.Background(),
		`SELECT outcome FROM backup_runs WHERE kind = $1 ORDER BY started_at DESC LIMIT 1`, kind).Scan(&outcome)
	if err != nil {
		t.Fatalf("latest %s run lookup: %v", kind, err)
	}
	if outcome != wantOutcome {
		t.Fatalf("latest %s run outcome = %s, want %s", kind, outcome, wantOutcome)
	}
}

func assertNoEphemeralDBs(t *testing.T, ctx context.Context, migDSN string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, migDSN)
	if err != nil {
		t.Fatalf("connect for ephemeral check: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'atlas_restore_verify_%'`).Scan(&n); err != nil {
		t.Fatalf("ephemeral-db count: %v", err)
	}
	if n != 0 {
		t.Fatalf("P0-510-2 violated: %d ephemeral verify DB(s) left standing", n)
	}
}
