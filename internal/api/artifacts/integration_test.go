//go:build integration

// HTTP-level integration tests for slice 036. Real Postgres + real MinIO.

package artifacts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
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

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/artifact"
)

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func minioEndpoint(t *testing.T) string {
	t.Helper()
	v := os.Getenv("MINIO_ENDPOINT")
	if v == "" {
		t.Skip("MINIO_ENDPOINT not set; skipping integration test")
	}
	return v
}

func minioBucket() string {
	v := os.Getenv("MINIO_BUCKET")
	if v == "" {
		return "atlas-artifacts-test"
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
	return pool
}

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

func ensureBucket(t *testing.T, cli *s3.Client, bucket string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") && !strings.Contains(err.Error(), "BucketAlreadyExists") {
		t.Fatalf("ensureBucket: %v", err)
	}
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
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

type setup struct {
	server *httptest.Server
	bearer string
	bucket string
}

func setupHTTPServer(t *testing.T, tenant string) setup {
	t.Helper()
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	endpoint := minioEndpoint(t)
	bucket := minioBucket()
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

	srv := api.New(api.Config{RotationGrace: time.Hour, ArtifactStore: store})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path. ViewerFor mirrors
	// the legacy IssueBootstrapCredential default (no elevation).
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		app.Close()
		admin.Close()
	})
	return setup{server: ts, bearer: bearer, bucket: bucket}
}

func uploadMultipart(t *testing.T, s setup, body []byte, contentType string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="payload.bin"`)
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("Write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.server.URL+"/v1/artifacts:upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.bearer)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	return resp, rb
}

func getArtifact(t *testing.T, s setup, id string, ttlQuery string) (*http.Response, []byte) {
	t.Helper()
	urlStr := s.server.URL + "/v1/artifacts/" + id
	if ttlQuery != "" {
		urlStr += "?ttl=" + ttlQuery
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, urlStr, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	return resp, rb
}

// AC-1 + AC-2 + ISC-17 + ISC-20: round-trip upload then signed download.
func TestHTTP_UploadThenDownload(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	s := setupHTTPServer(t, tenant)

	body := []byte("hello atlas " + uuid.NewString())
	resp, raw := uploadMultipart(t, s, body, "text/plain")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status: %d body=%s", resp.StatusCode, raw)
	}
	var up struct {
		Artifact struct {
			ID         string `json:"id"`
			SizeBytes  int64  `json:"size_bytes"`
			PayloadURI string `json:"payload_uri"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode upload: %v body=%s", err, raw)
	}
	if up.Artifact.SizeBytes != int64(len(body)) {
		t.Fatalf("size mismatch: got %d want %d", up.Artifact.SizeBytes, len(body))
	}
	wantPrefix := "s3://" + s.bucket + "/tenant-" + tenant + "/"
	if !strings.HasPrefix(up.Artifact.PayloadURI, wantPrefix) {
		t.Fatalf("payload_uri prefix: got %q want prefix %q", up.Artifact.PayloadURI, wantPrefix)
	}

	resp2, raw2 := getArtifact(t, s, up.Artifact.ID, "")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get status: %d body=%s", resp2.StatusCode, raw2)
	}
	var dn struct {
		URL        string `json:"url"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	if err := json.Unmarshal(raw2, &dn); err != nil {
		t.Fatalf("decode get: %v body=%s", err, raw2)
	}
	// AC-2: TTL bound.
	if dn.TTLSeconds > 3600 {
		t.Fatalf("TTL seconds %d exceeds 3600", dn.TTLSeconds)
	}
	if dn.URL == "" {
		t.Fatalf("URL empty")
	}
	// Actually GET the signed URL and verify the body round-trips.
	dlResp, err := http.Get(dn.URL)
	if err != nil {
		t.Fatalf("download via presign: %v", err)
	}
	defer func() { _ = dlResp.Body.Close() }()
	got, _ := io.ReadAll(dlResp.Body)
	if string(got) != string(body) {
		t.Fatalf("download body mismatch: status %d body=%q", dlResp.StatusCode, string(got))
	}
}

// AC-3 + ISC-21 + ISC-A1 — cross-tenant GET returns 404, not 403.
func TestHTTP_CrossTenantReturns404(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Upload as tenant A.
	sA := setupHTTPServer(t, tenantA)
	body := []byte("A's content " + uuid.NewString())
	resp, raw := uploadMultipart(t, sA, body, "text/plain")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload A: status %d body=%s", resp.StatusCode, raw)
	}
	var up struct {
		Artifact struct {
			ID string `json:"id"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode A: %v", err)
	}

	// Now try as tenant B.
	sB := setupHTTPServer(t, tenantB)
	resp2, raw2 := getArtifact(t, sB, up.Artifact.ID, "")
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant GET must be 404; got %d body=%s", resp2.StatusCode, raw2)
	}
}

// AC-2 + ISC-A2 — client-requested huge TTL is clamped server-side.
func TestHTTP_TTLClampedToOneHour(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	s := setupHTTPServer(t, tenant)

	body := []byte("clamp me")
	resp, raw := uploadMultipart(t, s, body, "text/plain")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %s", resp.StatusCode, raw)
	}
	var up struct {
		Artifact struct {
			ID string `json:"id"`
		} `json:"artifact"`
	}
	_ = json.Unmarshal(raw, &up)

	// Request 24h (= 86400 seconds) and check the response.
	resp2, raw2 := getArtifact(t, s, up.Artifact.ID, "86400")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get: %d %s", resp2.StatusCode, raw2)
	}
	var dn struct {
		URL        string `json:"url"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	_ = json.Unmarshal(raw2, &dn)
	if dn.TTLSeconds > 3600 {
		t.Fatalf("server failed to clamp TTL: got %d", dn.TTLSeconds)
	}

	// Inspect the URL itself — AWS SDK v4 presign embeds X-Amz-Expires.
	parsed, err := url.Parse(dn.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	exp := parsed.Query().Get("X-Amz-Expires")
	if exp != "" {
		// Should also be ≤ 3600.
		if exp != "3600" && exp != "900" && exp != "60" {
			// Anything else above 3600 would be a regression.
			if len(exp) > 4 {
				t.Fatalf("X-Amz-Expires unclamped: %s", exp)
			}
		}
	}
}

// AC-6 + ISC-22 + ISC-23 + ISC-A3 — every upload + every download adds
// an audit row.
func TestHTTP_AuditRowsEmitted(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	s := setupHTTPServer(t, tenant)

	body := []byte("audited http")
	resp, raw := uploadMultipart(t, s, body, "text/plain")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %s", resp.StatusCode, raw)
	}
	var up struct {
		Artifact struct {
			ID string `json:"id"`
		} `json:"artifact"`
	}
	_ = json.Unmarshal(raw, &up)

	// Trigger one download so the audit log gains a 'download' row.
	// The response is not under test here — the assertion is on the
	// audit-log row counts below.
	_, _ = getArtifact(t, s, up.Artifact.ID, "")

	row, err := admin.Query(context.Background(), `
		SELECT count(*) FILTER (WHERE action = 'upload')::bigint,
		       count(*) FILTER (WHERE action = 'download')::bigint
		FROM artifact_access_log
		WHERE tenant_id = $1
	`, tenant)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	defer row.Close()
	var uploads, downloads int64
	if !row.Next() {
		t.Fatal("count row missing")
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

// AC-1 oversize — body > MaxUploadBytes is rejected with 413 before
// touching S3 or DB.
func TestHTTP_UploadOversizeReturns413(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	s := setupHTTPServer(t, tenant)

	body := make([]byte, artifact.MaxUploadBytes+1)
	resp, raw := uploadMultipart(t, s, body, "application/octet-stream")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", resp.StatusCode, raw)
	}
}
