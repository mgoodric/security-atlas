//go:build integration

// Slice 011 — HTTP-level integration tests for the manual control
// attestation endpoint. Real Postgres + real schema registry + real
// slice-013 ingest service. The optional artifact upload path is only
// exercised when MINIO_ENDPOINT is set; otherwise we skip the
// artifact_id sub-tests but still cover the no-file happy path.
//
// Mirrors the scaffold in internal/api/artifacts/integration_test.go.

package controls_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
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

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// ----- env helpers -----

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

// ----- tenant + SCF anchor scaffolding (mirrors slice-009) -----

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func seedSCFAnchor(t *testing.T, admin *pgxpool.Pool, code, family string) string {
	t.Helper()
	ctx := context.Background()

	var frameworkID uuid.UUID
	err := admin.QueryRow(ctx, `
		SELECT id FROM frameworks WHERE slug = 'scf' AND tenant_id IS NULL
	`).Scan(&frameworkID)
	if errors.Is(err, pgx.ErrNoRows) {
		frameworkID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
			VALUES ($1, NULL, 'scf', 'Secure Controls Framework', 'SCF Council', '')
		`, frameworkID); err != nil {
			t.Fatalf("insert framework: %v", err)
		}
	} else if err != nil {
		t.Fatalf("lookup framework: %v", err)
	}

	var versionID uuid.UUID
	err = admin.QueryRow(ctx, `
		SELECT id FROM framework_versions
		WHERE framework_id = $1 AND status = 'current'
	`, frameworkID).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		versionID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_versions
				(id, tenant_id, framework_id, version, status)
			VALUES ($1, NULL, $2, 'test-1.0', 'current')
		`, versionID, frameworkID); err != nil {
			t.Fatalf("insert framework_version: %v", err)
		}
	} else if err != nil {
		t.Fatalf("lookup framework_version: %v", err)
	}

	id := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (framework_version_id, scf_id) DO NOTHING
	`, id, versionID, code, family, "Test anchor "+code); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
	return code
}

// ----- schema registry bootstrap (mirrors slice-013) -----

func bootRegistry(t *testing.T, admin *pgxpool.Pool) *schemaregistry.Service {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin conn: %v", err)
	}
	defer conn.Release()
	// Best-effort advisory lock; we don't strictly need to block
	// parallel test packages because the registry seed is idempotent
	// (ON CONFLICT DO NOTHING). The lock keeps log output clean.
	_, _ = conn.Exec(ctx, "SELECT pg_advisory_lock(6502261335191781140)")
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(6502261335191781140)")
	}()

	platform, err := schemaregistry.LoadPlatformSchemas(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("LoadPlatformSchemas: %v", err)
	}
	for _, ps := range platform {
		anchors := ps.DefaultSCFAnchors
		if anchors == nil {
			anchors = []string{}
		}
		// parseSemverParts inline (avoid cross-package helper).
		major, minor, patch, perr := parseSemverParts(ps.Semver)
		if perr != nil {
			t.Fatalf("parse semver %s: %v", ps.Semver, perr)
		}
		_, err := conn.Exec(ctx, `
			INSERT INTO evidence_kind_schemas
				(id, tenant_id, kind, semver, major, minor, patch,
				 schema_json, owner, default_scf_anchors, created_by)
			VALUES
				(gen_random_uuid(), NULL, $1, $2, $3, $4, $5,
				 $6::jsonb, $7, $8, 'slice-011-test-bootstrap')
			ON CONFLICT (kind, semver) WHERE tenant_id IS NULL DO NOTHING
		`, ps.Kind, ps.Semver, major, minor, patch,
			string(ps.SchemaJSON), ps.Owner, anchors)
		if err != nil {
			t.Fatalf("seed %s/%s: %v", ps.Kind, ps.Semver, err)
		}
	}

	reg := schemaregistry.NewService(admin)
	if err := reg.LoadFromDB(ctx); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if !reg.IsRegistered("manual.attestation.v1", "1.1.0") {
		t.Fatalf("boot: manual.attestation.v1/1.1.0 missing from cache")
	}
	return reg
}

func parseSemverParts(s string) (major, minor, patch int, err error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("semver must have three parts: %s", s)
	}
	_, err = fmt.Sscanf(s, "%d.%d.%d", &major, &minor, &patch)
	return
}

// ----- optional MinIO scaffolding (skip-aware) -----

func optionalArtifactStore(t *testing.T, app *pgxpool.Pool) *artifact.Store {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		return nil
	}
	bucket := os.Getenv("MINIO_BUCKET")
	if bucket == "" {
		bucket = "atlas-artifacts-test"
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
	if _, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		if !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") &&
			!strings.Contains(err.Error(), "BucketAlreadyExists") {
			t.Fatalf("CreateBucket: %v", err)
		}
	}
	store, err := artifact.NewStore(artifact.Config{
		Pool:      app,
		S3:        cli,
		Presigner: &artifact.S3Presigner{Bucket: bucket, Client: s3.NewPresignClient(cli)},
		Bucket:    bucket,
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// ----- server harness -----

type setupResult struct {
	server    *httptest.Server
	adminBear string
	ownerBear string
	plainBear string
	tenant    string
	registry  *schemaregistry.Service
	hasMinIO  bool
	bucket    string
}

const ownerRole = "control_owner"

// setupHTTP builds an HTTP test server with auth, ingest, registry,
// optional artifact store. Three pre-issued credentials are returned:
// admin, owner (holding control_owner role), and plain (no role).
func setupHTTP(t *testing.T) setupResult {
	t.Helper()
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	reg := bootRegistry(t, admin)
	seedSCFAnchor(t, admin, "IAC-06", "IAC")
	tenant := freshTenant(t, admin)

	ingester := ingest.New(app, reg)
	store := optionalArtifactStore(t, app)
	bucket := ""
	if store != nil {
		bucket = store.Bucket()
	}

	srv := api.New(api.Config{
		RotationGrace:  time.Hour,
		SchemaRegistry: reg,
		IngestService:  ingester,
		ArtifactStore:  store,
	})
	srv.AttachDB(app)

	_, adminBear, err := srv.IssueBootstrapAdminCredential(tenant)
	if err != nil {
		t.Fatalf("IssueBootstrapAdminCredential: %v", err)
	}
	_, ownerBear, err := srv.IssueBootstrapOwnerCredential(tenant, []string{ownerRole})
	if err != nil {
		t.Fatalf("IssueBootstrapOwnerCredential: %v", err)
	}
	_, plainBear, err := srv.IssueBootstrapCredential(tenant)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}

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
	return setupResult{
		server:    ts,
		adminBear: adminBear,
		ownerBear: ownerBear,
		plainBear: plainBear,
		tenant:    tenant,
		registry:  reg,
		hasMinIO:  store != nil,
		bucket:    bucket,
	}
}

// uploadBundle posts a manual control bundle and returns the new
// control id.
func uploadBundle(t *testing.T, s setupResult, bundleID string) string {
	t.Helper()
	yaml := `bundle_schema_version: "1"
bundle_id: ` + bundleID + `
title: "Manual review of access list"
implementation_type: manual_attested
scf_anchor_id: IAC-06
owner_role: ` + ownerRole + `
freshness_class: quarterly
manual_evidence_schema:
  type: object
  required: [reviewer]
  properties:
    reviewer:
      type: string
      minLength: 1
    notes:
      type: string
`
	body, _ := json.Marshal(map[string]string{"manifest_yaml": yaml})
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, s.server.URL+"/v1/controls:upload-bundle", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.adminBear)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("upload bundle: status %d body=%s", resp.StatusCode, raw)
	}
	var up struct {
		ControlID string `json:"control_id"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode upload: %v body=%s", err, raw)
	}
	return up.ControlID
}

func uploadArtifact(t *testing.T, s setupResult, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="evidence.pdf"`)
	h.Set("Content-Type", "application/pdf")
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, s.server.URL+"/v1/artifacts:upload", &buf)
	req.Header.Set("Authorization", "Bearer "+s.ownerBear)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload artifact: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload artifact: status %d body=%s", resp.StatusCode, raw)
	}
	var up struct {
		Artifact struct {
			ID         string `json:"id"`
			PayloadURI string `json:"payload_uri"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode artifact: %v body=%s", err, raw)
	}
	wantPrefix := "s3://" + s.bucket + "/tenant-" + s.tenant + "/"
	if !strings.HasPrefix(up.Artifact.PayloadURI, wantPrefix) {
		t.Fatalf("artifact payload_uri prefix: got %q want %q", up.Artifact.PayloadURI, wantPrefix)
	}
	return up.Artifact.ID
}

// httpJSON posts a JSON body to path and returns (status, raw body).
func httpJSON(t *testing.T, s setupResult, method, path, bearer string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, s.server.URL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// ----- tests -----

// AC-2 + AC-6 + ISC-7..ISC-12, ISC-24..ISC-26: full no-file happy path.
func TestAttest_HappyPath_NoArtifact(t *testing.T) {
	s := setupHTTP(t)
	controlID := uploadBundle(t, s, "attest_happy_noart")

	body := map[string]any{
		"statement": "I attest the quarterly access review was completed on time.",
		"attestation_data": map[string]any{
			"reviewer": "matt@example.com",
			"notes":    "no exceptions found",
		},
	}
	status, raw := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.ownerBear, body)
	if status != http.StatusCreated {
		t.Fatalf("expected 201; got %d body=%s", status, raw)
	}
	var resp struct {
		RecordID     string `json:"record_id"`
		Hash         string `json:"hash"`
		IngestedAt   string `json:"ingested_at"`
		CredentialID string `json:"credential_id"`
		Deduplicated bool   `json:"deduplicated"`
		PayloadURI   string `json:"payload_uri"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode resp: %v body=%s", err, raw)
	}
	if resp.RecordID == "" || resp.Hash == "" {
		t.Fatalf("record_id+hash must be populated: %+v", resp)
	}
	if resp.PayloadURI != "" {
		t.Fatalf("no-artifact path must yield empty payload_uri; got %q", resp.PayloadURI)
	}

	// Cross-check the ledger directly.
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	var (
		kind          *string
		actorAttrib   []byte
		ingestionPath string
		payloadURI    *string
		hash          string
	)
	if err := admin.QueryRow(context.Background(), `
		SELECT evidence_kind, source_attribution, ingestion_path, payload_uri, hash
		FROM evidence_records WHERE id = $1
	`, resp.RecordID).Scan(&kind, &actorAttrib, &ingestionPath, &payloadURI, &hash); err != nil {
		t.Fatalf("verify row: %v", err)
	}
	if kind == nil || *kind != "manual.attestation.v1" {
		t.Fatalf("evidence_kind mismatch: %v", kind)
	}
	if ingestionPath != "manual_upload" {
		t.Fatalf("ingestion_path: got %q want manual_upload", ingestionPath)
	}
	if payloadURI != nil {
		t.Fatalf("payload_uri must be NULL when no artifact; got %q", *payloadURI)
	}
	if hash == "" {
		t.Fatalf("hash must be populated")
	}
	var src map[string]any
	if err := json.Unmarshal(actorAttrib, &src); err != nil {
		t.Fatalf("decode source_attribution: %v", err)
	}
	if got, _ := src["actor_type"].(string); got != "human" {
		t.Fatalf("actor_type: got %q want human", got)
	}
	if got, _ := src["actor_id"].(string); got == "" || !strings.HasPrefix(got, "key_") {
		t.Fatalf("actor_id must equal owner credential UserID; got %q", got)
	}

	// AC-6: audit-log row exists, decision=accepted.
	var decision, credID string
	var auditKind *string
	if err := admin.QueryRow(context.Background(), `
		SELECT decision, credential_id, evidence_kind
		FROM evidence_audit_log
		WHERE record_id = $1
	`, resp.RecordID).Scan(&decision, &credID, &auditKind); err != nil {
		t.Fatalf("verify audit: %v", err)
	}
	if decision != "accepted" {
		t.Fatalf("audit decision: got %q want accepted", decision)
	}
	if !strings.HasPrefix(credID, "key_") {
		t.Fatalf("audit credential_id: got %q", credID)
	}
	if auditKind == nil || *auditKind != "manual.attestation.v1" {
		t.Fatalf("audit evidence_kind: %v", auditKind)
	}
}

// AC-3 + ISC-14..ISC-16: artifact upload path. Skips when MinIO is absent.
func TestAttest_HappyPath_WithArtifact(t *testing.T) {
	s := setupHTTP(t)
	if !s.hasMinIO {
		t.Skip("MINIO_ENDPOINT not set; skipping artifact path")
	}
	controlID := uploadBundle(t, s, "attest_happy_art")
	artifactID := uploadArtifact(t, s, []byte("pretend PDF bytes "+uuid.NewString()))

	body := map[string]any{
		"statement":   "I attest with attached signed PDF.",
		"artifact_id": artifactID,
		"attestation_data": map[string]any{
			"reviewer": "matt@example.com",
		},
	}
	status, raw := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.ownerBear, body)
	if status != http.StatusCreated {
		t.Fatalf("expected 201; got %d body=%s", status, raw)
	}
	var resp struct {
		PayloadURI string `json:"payload_uri"`
	}
	_ = json.Unmarshal(raw, &resp)
	wantPrefix := "s3://" + s.bucket + "/tenant-" + s.tenant + "/"
	if !strings.HasPrefix(resp.PayloadURI, wantPrefix) {
		t.Fatalf("payload_uri prefix: got %q want %q", resp.PayloadURI, wantPrefix)
	}
}

// AC-5 + ISC-19: owner-role gate rejects callers without the role.
func TestAttest_Rejects_WhenCallerLacksOwnerRole(t *testing.T) {
	s := setupHTTP(t)
	controlID := uploadBundle(t, s, "attest_no_role")
	body := map[string]any{
		"statement": "I shouldn't be allowed to do this.",
		"attestation_data": map[string]any{
			"reviewer": "matt@example.com",
		},
	}
	status, raw := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.plainBear, body)
	if status != http.StatusForbidden {
		t.Fatalf("expected 403; got %d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), ownerRole) {
		t.Fatalf("403 body should name the owner_role; got %s", raw)
	}
}

// AC-5 + ISC-21: admin acts as wildcard for any owner_role.
func TestAttest_Admin_IsWildcard(t *testing.T) {
	s := setupHTTP(t)
	controlID := uploadBundle(t, s, "attest_admin_ok")
	body := map[string]any{
		"statement": "Admin attestation.",
		"attestation_data": map[string]any{
			"reviewer": "admin@example.com",
		},
	}
	status, raw := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.adminBear, body)
	if status != http.StatusCreated {
		t.Fatalf("expected 201; got %d body=%s", status, raw)
	}
}

// ISC-13: idempotency-key reuse with same payload returns dedup receipt.
// The body must be byte-identical across retries; we pin observed_at so
// the handler doesn't mint a fresh time.Now() and shift the canonical
// hash, which would (correctly) trip the idempotency-mismatch path.
func TestAttest_Idempotency_Dedup(t *testing.T) {
	s := setupHTTP(t)
	controlID := uploadBundle(t, s, "attest_idem")
	body := map[string]any{
		"statement":       "Same attestation twice.",
		"idempotency_key": "manual-test-" + uuid.NewString()[:8],
		"observed_at":     time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano),
		"attestation_data": map[string]any{
			"reviewer": "matt@example.com",
		},
	}
	status1, raw1 := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.ownerBear, body)
	if status1 != http.StatusCreated {
		t.Fatalf("first: expected 201; got %d body=%s", status1, raw1)
	}
	var r1 struct {
		RecordID     string `json:"record_id"`
		Deduplicated bool   `json:"deduplicated"`
	}
	_ = json.Unmarshal(raw1, &r1)
	if r1.Deduplicated {
		t.Fatalf("first request must not be deduplicated")
	}
	status2, raw2 := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.ownerBear, body)
	if status2 != http.StatusOK {
		t.Fatalf("second: expected 200 (dedup); got %d body=%s", status2, raw2)
	}
	var r2 struct {
		RecordID     string `json:"record_id"`
		Deduplicated bool   `json:"deduplicated"`
	}
	_ = json.Unmarshal(raw2, &r2)
	if !r2.Deduplicated {
		t.Fatalf("second request must be deduplicated")
	}
	if r1.RecordID != r2.RecordID {
		t.Fatalf("dedup must return same record_id; got %q vs %q", r1.RecordID, r2.RecordID)
	}
}

// AC-1 + ISC-1..ISC-4: GET form metadata.
func TestAttestForm_HappyPath(t *testing.T) {
	s := setupHTTP(t)
	controlID := uploadBundle(t, s, "attest_form_ok")
	status, raw := httpJSON(t, s, http.MethodGet,
		"/v1/controls/"+controlID+"/attest-form", s.ownerBear, nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", status, raw)
	}
	var form struct {
		ControlID            string         `json:"control_id"`
		OwnerRole            string         `json:"owner_role"`
		ImplementationType   string         `json:"implementation_type"`
		ManualEvidenceSchema map[string]any `json:"manual_evidence_schema"`
		CallerCanAttest      bool           `json:"caller_can_attest"`
	}
	if err := json.Unmarshal(raw, &form); err != nil {
		t.Fatalf("decode form: %v body=%s", err, raw)
	}
	if form.OwnerRole != ownerRole {
		t.Fatalf("owner_role: got %q want %q", form.OwnerRole, ownerRole)
	}
	if form.ImplementationType != "manual_attested" {
		t.Fatalf("implementation_type: got %q", form.ImplementationType)
	}
	if !form.CallerCanAttest {
		t.Fatalf("owner must be able to attest")
	}
	if form.ManualEvidenceSchema == nil {
		t.Fatalf("manual_evidence_schema absent")
	}
}

// ISC-3: cross-tenant control id → 404.
func TestAttest_CrossTenant_NotFound(t *testing.T) {
	s := setupHTTP(t)
	// Bundle is owned by tenant A; bearer below is for tenant B.
	controlID := uploadBundle(t, s, "attest_cross_tenant")

	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenantB := freshTenant(t, admin)
	ingest := ingest.New(openPool(t, appDSN(t)), s.registry)
	srvB := api.New(api.Config{
		RotationGrace:  time.Hour,
		SchemaRegistry: s.registry,
		IngestService:  ingest,
	})
	srvB.AttachDB(openPool(t, appDSN(t)))
	_, bearerB, err := srvB.IssueBootstrapOwnerCredential(tenantB, []string{ownerRole})
	if err != nil {
		t.Fatalf("bearer B: %v", err)
	}
	h := srvB.HTTPHandlerForTests()
	tsB := httptest.NewServer(h)
	defer tsB.Close()

	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodGet, tsB.URL+"/v1/controls/"+controlID+"/attest-form", nil)
	req.Header.Set("Authorization", "Bearer "+bearerB)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get cross-tenant: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404; got %d body=%s", resp.StatusCode, raw)
	}
}

// ISC-27: schema-validation rejects attestation_data missing a required
// per-control field (the control declares `reviewer` required).
func TestAttest_Rejects_MissingRequiredAttestationData(t *testing.T) {
	s := setupHTTP(t)
	controlID := uploadBundle(t, s, "attest_missing_req")
	body := map[string]any{
		"statement": "Missing reviewer field.",
		"attestation_data": map[string]any{
			// reviewer omitted on purpose
			"notes": "I forgot the reviewer.",
		},
	}
	status, raw := httpJSON(t, s, http.MethodPost,
		"/v1/controls/"+controlID+"/attestations", s.ownerBear, body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "reviewer") {
		t.Fatalf("error must mention reviewer; got %s", raw)
	}
}
