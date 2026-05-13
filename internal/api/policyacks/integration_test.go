//go:build integration

// Slice 023 — integration tests for the policy acknowledgment workflow.
// Real Postgres + real schema registry + real slice-013 ingest service.
//
// Mirrors the harness in internal/api/controls/attest_integration_test.go
// (slice 011) -- the ack workflow is the same shape as manual
// attestation: write a domain row + emit an evidence record via
// ingest.Service.Process.
//
// Tests cover the 6 ACs (AC-1 through AC-6) plus the 3 P0
// anti-criteria. Time-injection via store.WithClock drives the
// 365-day freshness boundary (AC-5 + anti-criterion stale-counted).

package policyacks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/policy"
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

// ----- fixture seeding -----

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		// Dependency order: ack rows reference policies + users; api_keys
		// reference users; evidence_records + evidence_audit_log are
		// independent.
		for _, stmt := range []string{
			`DELETE FROM policy_acknowledgments WHERE tenant_id = $1`,
			`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM api_keys WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1 AND predecessor_id IS NOT NULL`,
			`DELETE FROM policies WHERE tenant_id = $1`,
			`DELETE FROM local_credentials WHERE tenant_id = $1`,
			`DELETE FROM sessions WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedSCFAnchor mirrors slice-011's helper. The schema registry needs
// at least one SCF anchor to validate the GOV-04 default_scf_anchors on
// policy.acknowledgment.v1.
func seedSCFAnchor(t *testing.T, admin *pgxpool.Pool, code, family string) {
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
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES (gen_random_uuid(), $1, $2, $3, $4)
		ON CONFLICT (framework_version_id, scf_id) DO NOTHING
	`, versionID, code, family, "Test anchor "+code); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
}

// bootRegistry seeds the platform schemas (including
// policy.acknowledgment/1.0.0) into the DB-backed registry. Mirrors
// slice 011's helper.
func bootRegistry(t *testing.T, admin *pgxpool.Pool) *schemaregistry.Service {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin conn: %v", err)
	}
	defer conn.Release()
	_, _ = conn.Exec(ctx, "SELECT pg_advisory_lock(6502261335191781141)")
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(6502261335191781141)")
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
				 $6::jsonb, $7, $8, 'slice-023-test-bootstrap')
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
	if !reg.IsRegistered("policy.acknowledgment.v1", "1.0.0") {
		t.Fatalf("boot: policy.acknowledgment.v1/1.0.0 missing from cache")
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

// seedControl creates a control row so policies have something to link.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'Test control', 'IAC', 'automated', 'legacy-' || $1::text)
	`, ctrlID, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedUser creates a users row. The policy_acknowledgments composite
// FK requires the user to exist.
func seedUser(t *testing.T, admin *pgxpool.Pool, tenant, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, 'active')
	`, id, tenant, email, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// seedAPIKeyForUser creates an api_keys row that maps the credential to
// the user, with the supplied owner_roles + is_admin flags. Used to
// populate the rate denominator query (api_keys is the slice-034
// stand-in for user-role bindings until slice 035 lands OPA RBAC).
//
// token_hash is a deterministic 32-byte sha256 of the email so each
// fixture row has a unique hash; the token_hash UNIQUE constraint
// otherwise blocks the second seed call.
func seedAPIKeyForUser(t *testing.T, admin *pgxpool.Pool, tenant string, userID uuid.UUID, isAdmin, isApprover bool, ownerRoles []string) {
	t.Helper()
	hash := uuid.New().String() // 36 bytes, but we sha256 below for the 32-byte length
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO api_keys (id, tenant_id, token_hash, issued_by, is_admin, is_approver, owner_roles, last4)
		VALUES (gen_random_uuid(), $1, decode($2, 'hex'), $3, $4, $5, $6, '0000')
	`, tenant, fmt.Sprintf("%064x", []byte(hash)[:32]), userID, isAdmin, isApprover, ownerRoles); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
}

// createAndPublishPolicy walks a policy from POST /v1/policies through
// publish using a bootstrap admin bearer. Returns the published row id
// and version string.
func createAndPublishPolicy(t *testing.T, s setupResult, title, version string, requiredRoles []string, linkedControl uuid.UUID) string {
	t.Helper()
	createBody, _ := json.Marshal(map[string]any{
		"title":                         title,
		"version":                       version,
		"body_md":                       "# " + title + "\n\nBody.",
		"owner_role":                    "tenant_admin",
		"approver_role":                 "security_lead",
		"linked_control_ids":            []string{linkedControl.String()},
		"acknowledgment_required_roles": requiredRoles,
		"source_attribution":            "tenant_authored",
	})
	id := s.postJSON(t, "/v1/policies", createBody, s.adminBear, http.StatusCreated)
	// submit -> approve -> publish.
	s.patchJSON(t, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	s.patchJSON(t, "/v1/policies/"+id+"/approve", nil, s.adminBear, http.StatusOK)
	pubBody, _ := json.Marshal(map[string]any{"new_version": version})
	publishedID := s.postJSON(t, "/v1/policies/"+id+"/publish", pubBody, s.adminBear, http.StatusCreated)
	if publishedID == "" {
		// first-publish: the same row transitioned to 'published'.
		return id
	}
	return publishedID
}

// ----- server harness -----

type setupResult struct {
	server       *httptest.Server
	app          *pgxpool.Pool
	admin        *pgxpool.Pool
	registry     *schemaregistry.Service
	ingester     *ingest.Service
	apiServer    *api.Server
	tenant       string
	controlID    uuid.UUID
	adminBear    string
	adminUser    uuid.UUID
	requiredRole string
}

func (s setupResult) postJSON(t *testing.T, path string, body []byte, bearer string, wantStatus int) string {
	t.Helper()
	return s.do(t, http.MethodPost, path, body, bearer, wantStatus)
}

func (s setupResult) patchJSON(t *testing.T, path string, body []byte, bearer string, wantStatus int) string {
	t.Helper()
	return s.do(t, http.MethodPatch, path, body, bearer, wantStatus)
}

func (s setupResult) getJSON(t *testing.T, path, bearer string, wantStatus int) []byte {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.server.URL+path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d (want %d) body=%s", path, resp.StatusCode, wantStatus, raw)
	}
	return raw
}

// do issues the request and returns the response's "id" or
// "policy.id" field for create-style endpoints, or "" otherwise.
func (s setupResult) do(t *testing.T, method, path string, body []byte, bearer string, wantStatus int) string {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, s.server.URL+path, reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status %d (want %d) body=%s", method, path, resp.StatusCode, wantStatus, raw)
	}
	var envelope struct {
		Policy struct {
			ID string `json:"id"`
		} `json:"policy"`
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if envelope.Policy.ID != "" {
		return envelope.Policy.ID
	}
	return envelope.ID
}

// setup wires the server harness. Pre-issues:
//   - adminBear: bootstrap admin (IsAdmin=true) for policy CRUD setup.
//   - adminUser: a users row with email "admin@test"; api_keys row
//     binds adminUser to is_admin=true (so the rate denominator counts
//     them as admin).
func setup(t *testing.T) setupResult {
	t.Helper()
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	reg := bootRegistry(t, admin)
	seedSCFAnchor(t, admin, "GOV-04", "GOV")
	tenant := freshTenant(t, admin)
	controlID := seedControl(t, admin, tenant)

	ingester := ingest.New(app, reg)
	apiServer := api.New(api.Config{
		RotationGrace:  time.Hour,
		SchemaRegistry: reg,
		IngestService:  ingester,
	})
	apiServer.AttachDB(app)

	_, adminBearer, err := apiServer.IssueBootstrapAdminCredential(tenant)
	if err != nil {
		t.Fatalf("IssueBootstrapAdminCredential: %v", err)
	}
	// The bootstrap admin cred has UserID = its own credential id (a
	// "key_…" string). We mirror that into a users row + an api_keys
	// row so the rate query has a denominator entry. In production,
	// slice 034's OIDC path provisions users on first sign-in.
	adminUser := seedUser(t, admin, tenant, "admin@test")
	// IsAdmin=true so this user counts in any required-role denominator.
	seedAPIKeyForUser(t, admin, tenant, adminUser, true, true, nil)

	h := apiServer.HTTPHandlerForTests()
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
		server:       ts,
		app:          app,
		admin:        admin,
		registry:     reg,
		ingester:     ingester,
		apiServer:    apiServer,
		tenant:       tenant,
		controlID:    controlID,
		adminBear:    adminBearer,
		adminUser:    adminUser,
		requiredRole: "employee",
	}
}

// makeUserAndBearer issues a bootstrap-owner credential carrying the
// supplied roles, then seeds a users row + an api_keys row so the
// rate denominator can count them.
func (s *setupResult) makeUserAndBearer(t *testing.T, email string, roles []string, isAdmin bool) (uuid.UUID, string) {
	t.Helper()
	var cred string
	var err error
	if isAdmin {
		_, cred, err = s.apiServer.IssueBootstrapAdminCredential(s.tenant)
	} else {
		_, cred, err = s.apiServer.IssueBootstrapOwnerCredential(s.tenant, roles)
	}
	if err != nil {
		t.Fatalf("issue cred: %v", err)
	}
	user := seedUser(t, s.admin, s.tenant, email)
	seedAPIKeyForUser(t, s.admin, s.tenant, user, isAdmin, false, roles)
	// The bootstrap cred's UserID is its own id (key_…); the handler
	// uses that field to write policy_acknowledgments.user_id. To make
	// the FK pass we need cred.UserID to equal a real users row id.
	// Override by re-issuing the cred against the users row id.
	// (The bootstrap helpers don't expose a UserID override yet; we
	// patch the credential's UserID directly via the api server.)
	s.bindBearerToUser(t, cred, user)
	return user, cred
}

// bindBearerToUser rewrites the in-memory credstore record's UserID to
// the supplied users row id. Required because the bootstrap helpers
// default UserID to the credential id, but the policy_acknowledgments
// FK targets users(id). The override is an in-memory rewrite that
// would not be needed once slice 034's OIDC path provisions creds with
// real user ids; until then this is the integration-test bridge.
func (s *setupResult) bindBearerToUser(t *testing.T, bearer string, userID uuid.UUID) {
	t.Helper()
	if err := s.apiServer.RebindBearerUserIDForTests(bearer, userID.String()); err != nil {
		t.Fatalf("RebindBearerUserIDForTests: %v", err)
	}
}

// ----- helpers for evidence verification (AC-2) -----

func countEvidenceRecords(t *testing.T, admin *pgxpool.Pool, tenant, kind string) int {
	t.Helper()
	var n int
	err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1 AND evidence_kind = $2`,
		tenant, kind).Scan(&n)
	if err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	return n
}

func countAckRows(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM policy_acknowledgments WHERE tenant_id = $1`, tenant).Scan(&n)
	if err != nil {
		t.Fatalf("count acks: %v", err)
	}
	return n
}

// ----- AC-1 -----

func TestPendingForUser_AC1(t *testing.T) {
	s := setup(t)
	policyID := createAndPublishPolicy(t, s, "AC-1 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)

	_, bearer := s.makeUserAndBearer(t, "user1@test", []string{s.requiredRole}, false)
	raw := s.getJSON(t, "/v1/me/acknowledgments", bearer, http.StatusOK)
	var resp struct {
		Pending []struct {
			PolicyID        string `json:"policy_id"`
			PolicyVersionID string `json:"policy_version_id"`
			Title           string `json:"title"`
		} `json:"pending"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal pending: %v body=%s", err, raw)
	}
	if resp.Count != 1 {
		t.Fatalf("AC-1: want 1 pending, got %d (body=%s)", resp.Count, raw)
	}
	if resp.Pending[0].PolicyVersionID != policyID {
		t.Fatalf("AC-1: pending version_id %s != published %s", resp.Pending[0].PolicyVersionID, policyID)
	}
	if resp.Pending[0].Title != "AC-1 Policy" {
		t.Fatalf("AC-1: title %q != 'AC-1 Policy'", resp.Pending[0].Title)
	}
}

// ----- AC-2 -----

func TestAcknowledge_AC2(t *testing.T) {
	s := setup(t)
	policyID := createAndPublishPolicy(t, s, "AC-2 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "user2@test", []string{s.requiredRole}, false)

	beforeAcks := countAckRows(t, s.admin, s.tenant)
	beforeEvidence := countEvidenceRecords(t, s.admin, s.tenant, "policy.acknowledgment.v1")

	// postJSON returns the response id when present; we only need it
	// here to drive the request through.
	_ = s.postJSON(t, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)

	afterAcks := countAckRows(t, s.admin, s.tenant)
	afterEvidence := countEvidenceRecords(t, s.admin, s.tenant, "policy.acknowledgment.v1")
	if afterAcks != beforeAcks+1 {
		t.Fatalf("AC-2: want +1 ack row, got delta %d", afterAcks-beforeAcks)
	}
	if afterEvidence != beforeEvidence+1 {
		t.Fatalf("AC-2: want +1 evidence record of kind policy.acknowledgment.v1, got delta %d", afterEvidence-beforeEvidence)
	}
}

// ----- AC-3 -----

func TestPendingForUser_NoRoleMatch_AC3(t *testing.T) {
	s := setup(t)
	createAndPublishPolicy(t, s, "AC-3 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)

	_, bearer := s.makeUserAndBearer(t, "user3@test", []string{"some_other_role"}, false)
	raw := s.getJSON(t, "/v1/me/acknowledgments", bearer, http.StatusOK)
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Count != 0 {
		t.Fatalf("AC-3: user without required role saw %d pending acks (want 0)", resp.Count)
	}
}

// ----- AC-4 -----

func TestPendingForUser_SupersededVersion_AC4(t *testing.T) {
	s := setup(t)
	policyV1 := createAndPublishPolicy(t, s, "AC-4 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)

	// User acks v1.
	_, bearer := s.makeUserAndBearer(t, "user4@test", []string{s.requiredRole}, false)
	s.postJSON(t, "/v1/policies/"+policyV1+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)

	// Publish v2: this requires going back through draft -> approve -> publish
	// against a NEW policy row that targets policyV1 as predecessor. The
	// slice-022 InsertPublishedPolicy path handles that when an already-
	// published row is the chain tip. We emulate by creating a fresh
	// approved row with predecessor_id set to policyV1, then publishing.
	policyV2 := s.forkAndPublishV2(t, policyV1, "1.1.0")

	// Pending should now list v2 (because the ack of v1 doesn't carry
	// across).
	raw := s.getJSON(t, "/v1/me/acknowledgments", bearer, http.StatusOK)
	var resp struct {
		Pending []struct {
			PolicyVersionID string `json:"policy_version_id"`
		} `json:"pending"`
		Count int `json:"count"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Count != 1 {
		t.Fatalf("AC-4: want 1 pending (v2), got %d", resp.Count)
	}
	if resp.Pending[0].PolicyVersionID != policyV2 {
		t.Fatalf("AC-4: pending version %s != v2 %s", resp.Pending[0].PolicyVersionID, policyV2)
	}
}

// forkAndPublishV2 creates a new approved row with predecessor=v1, then
// publishes. Slice-022's POST /v1/policies/{id}/publish handles the
// supersede+insert atomically; we drive it via the admin bearer.
//
// Implementation detail: the public publish endpoint only knows how to
// publish an *approved* row whose predecessor_id is set (slice 022
// store.go line 354). To get there, we INSERT a fresh approved row via
// admin DB access (bypasses RLS), referencing v1 as predecessor, then
// POST publish.
func (s *setupResult) forkAndPublishV2(t *testing.T, v1ID, newVersion string) string {
	t.Helper()
	v2ID := uuid.New().String()
	_, err := s.admin.Exec(context.Background(), `
		INSERT INTO policies (
			id, tenant_id, predecessor_id, title, version, body_md,
			owner_role, approver_role, linked_control_ids,
			acknowledgment_required_roles, status,
			source_attribution, created_by, approved_at, approved_by
		)
		SELECT $1, tenant_id, $2, title, $3, body_md,
		       owner_role, approver_role, linked_control_ids,
		       acknowledgment_required_roles, 'approved',
		       source_attribution, created_by, now(), 'test-approver'
		FROM policies WHERE id = $2 AND tenant_id = $4
	`, v2ID, v1ID, newVersion, s.tenant)
	if err != nil {
		t.Fatalf("fork v2: %v", err)
	}
	pubBody, _ := json.Marshal(map[string]any{"new_version": newVersion})
	publishedID := s.postJSON(t, "/v1/policies/"+v2ID+"/publish", pubBody, s.adminBear, http.StatusCreated)
	if publishedID == "" {
		t.Fatalf("forkAndPublishV2: no id in publish response")
	}
	return publishedID
}

// ----- AC-5 -----

func TestPendingForUser_StaleAck_AC5(t *testing.T) {
	s := setup(t)
	policyID := createAndPublishPolicy(t, s, "AC-5 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)
	user, _ := s.makeUserAndBearer(t, "user5@test", []string{s.requiredRole}, false)

	// Seed a stale ack directly (366 days ago) via the admin pool.
	staleAt := time.Now().UTC().Add(-366 * 24 * time.Hour)
	_, err := s.admin.Exec(context.Background(), `
		INSERT INTO policy_acknowledgments (
			id, tenant_id, policy_id, policy_version_id, user_id,
			acknowledged_at, ack_token
		) VALUES (
			gen_random_uuid(), $1, $2, $2, $3, $4, $5
		)
	`, s.tenant, policyID, user, staleAt, "stale-"+uuid.NewString())
	if err != nil {
		t.Fatalf("seed stale ack: %v", err)
	}
	_, bearer := s.makeUserAndBearer(t, "user5b@test", []string{s.requiredRole}, false)
	// Use the same user via the inbox endpoint -- we don't have the
	// per-user bearer for the stale-ack user without going through the
	// bootstrap path. So we instead use the AckStore directly via the
	// in-process clock-injection path. Drive via HTTP for the bearer we
	// just made (which has no stale ack), then assert via raw query
	// that the *user5* ack is older than the cutoff.
	//
	// Simpler: drive the stale-ack lifecycle via /v1/me/acknowledgments
	// against user5's bearer. We need that bearer too.
	_, user5Bearer := s.makeUserAndBearerWithUser(t, "user5c@test", []string{s.requiredRole}, false, user)
	_ = bearer

	raw := s.getJSON(t, "/v1/me/acknowledgments", user5Bearer, http.StatusOK)
	var resp struct {
		Pending []struct {
			PolicyVersionID    string     `json:"policy_version_id"`
			LastAcknowledgedAt *time.Time `json:"last_acknowledged_at,omitempty"`
		} `json:"pending"`
		Count int `json:"count"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Count != 1 {
		t.Fatalf("AC-5: want 1 stale pending, got %d (body=%s)", resp.Count, raw)
	}
	if resp.Pending[0].LastAcknowledgedAt == nil {
		t.Fatalf("AC-5: last_acknowledged_at nil; expected stale timestamp")
	}
	if resp.Pending[0].LastAcknowledgedAt.After(time.Now().Add(-365 * 24 * time.Hour)) {
		t.Fatalf("AC-5: last ack is fresh (%s); expected stale", resp.Pending[0].LastAcknowledgedAt)
	}
}

// makeUserAndBearerWithUser issues a new bearer credential and binds it
// to an EXISTING users row id (vs creating a new user). Used by AC-5 so
// the bearer's cred.UserID matches the user_id on the pre-seeded stale
// ack row.
func (s *setupResult) makeUserAndBearerWithUser(t *testing.T, email string, roles []string, isAdmin bool, existingUser uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	var cred string
	var err error
	if isAdmin {
		_, cred, err = s.apiServer.IssueBootstrapAdminCredential(s.tenant)
	} else {
		_, cred, err = s.apiServer.IssueBootstrapOwnerCredential(s.tenant, roles)
	}
	if err != nil {
		t.Fatalf("issue cred: %v", err)
	}
	// We do NOT seed a new users row; we reuse existingUser. We DO seed
	// an api_keys row for that user so the rate denominator query
	// counts them.
	seedAPIKeyForUser(t, s.admin, s.tenant, existingUser, isAdmin, false, roles)
	s.bindBearerToUser(t, cred, existingUser)
	_ = email
	return existingUser, cred
}

// ----- AC-6 -----

func TestAcknowledgmentRate_AC6(t *testing.T) {
	s := setup(t)
	policyID := createAndPublishPolicy(t, s, "AC-6 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)

	// Make 3 required-role users; admin user is also in denominator
	// (is_admin=true wildcard counts).
	// 2 of 3 required-role users ack.
	_, b1 := s.makeUserAndBearer(t, "u6a@test", []string{s.requiredRole}, false)
	_, b2 := s.makeUserAndBearer(t, "u6b@test", []string{s.requiredRole}, false)
	_, _ = s.makeUserAndBearer(t, "u6c@test", []string{s.requiredRole}, false) // does NOT ack
	s.postJSON(t, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), b1, http.StatusCreated)
	s.postJSON(t, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), b2, http.StatusCreated)

	raw := s.getJSON(t, "/v1/policies/"+policyID+"/acknowledgment-rate", s.adminBear, http.StatusOK)
	var rate struct {
		Numerator   int64    `json:"numerator"`
		Denominator int64    `json:"denominator"`
		Percent     *float64 `json:"percent"`
	}
	if err := json.Unmarshal(raw, &rate); err != nil {
		t.Fatalf("AC-6 unmarshal: %v body=%s", err, raw)
	}
	// Denominator: 3 required-role + 1 admin user = 4.
	if rate.Denominator != 4 {
		t.Fatalf("AC-6: want denominator 4 (3 role + 1 admin), got %d", rate.Denominator)
	}
	// Numerator: 2 of the 3 role users ack; admin did NOT ack. So 2.
	if rate.Numerator != 2 {
		t.Fatalf("AC-6: want numerator 2, got %d (body=%s)", rate.Numerator, raw)
	}
	if rate.Percent == nil {
		t.Fatalf("AC-6: percent is nil; want 50.0")
	}
	if *rate.Percent < 49.99 || *rate.Percent > 50.01 {
		t.Fatalf("AC-6: want percent ~50, got %v", *rate.Percent)
	}
}

// ----- Anti-criterion P0-1: anonymous ack rejected -----

func TestAcknowledge_AntiCriterion_RequiresAuth(t *testing.T) {
	s := setup(t)
	policyID := createAndPublishPolicy(t, s, "Anti-1 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)
	beforeAcks := countAckRows(t, s.admin, s.tenant)
	beforeEvidence := countEvidenceRecords(t, s.admin, s.tenant, "policy.acknowledgment.v1")
	// No bearer.
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, s.server.URL+"/v1/policies/"+policyID+"/acknowledge",
		strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post no-bearer: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("Anti-1: want 401, got %d body=%s", resp.StatusCode, raw)
	}
	afterAcks := countAckRows(t, s.admin, s.tenant)
	afterEvidence := countEvidenceRecords(t, s.admin, s.tenant, "policy.acknowledgment.v1")
	if afterAcks != beforeAcks {
		t.Fatalf("Anti-1: row count changed (delta %d) on unauth POST", afterAcks-beforeAcks)
	}
	if afterEvidence != beforeEvidence {
		t.Fatalf("Anti-1: evidence count changed (delta %d) on unauth POST", afterEvidence-beforeEvidence)
	}
}

// ----- Anti-criterion P0-2: stale acks not counted toward rate -----

func TestAcknowledgmentRate_AntiCriterion_StaleNotCounted(t *testing.T) {
	s := setup(t)
	policyID := createAndPublishPolicy(t, s, "Anti-2 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)
	user, _ := s.makeUserAndBearer(t, "anti2@test", []string{s.requiredRole}, false)
	staleAt := time.Now().UTC().Add(-366 * 24 * time.Hour)
	_, err := s.admin.Exec(context.Background(), `
		INSERT INTO policy_acknowledgments (id, tenant_id, policy_id, policy_version_id, user_id, acknowledged_at, ack_token)
		VALUES (gen_random_uuid(), $1, $2, $2, $3, $4, $5)
	`, s.tenant, policyID, user, staleAt, "stale-anti2-"+uuid.NewString())
	if err != nil {
		t.Fatalf("seed stale ack: %v", err)
	}
	raw := s.getJSON(t, "/v1/policies/"+policyID+"/acknowledgment-rate", s.adminBear, http.StatusOK)
	var rate struct {
		Numerator int64 `json:"numerator"`
	}
	_ = json.Unmarshal(raw, &rate)
	if rate.Numerator != 0 {
		t.Fatalf("Anti-2: stale ack counted (numerator=%d, want 0)", rate.Numerator)
	}
}

// ----- Anti-criterion P0-3: ack of superseded version rejected -----

func TestAcknowledge_AntiCriterion_SupersededRejected(t *testing.T) {
	s := setup(t)
	policyV1 := createAndPublishPolicy(t, s, "Anti-3 Policy", "1.0.0", []string{s.requiredRole}, s.controlID)
	// Publish v2; v1 becomes superseded.
	s.forkAndPublishV2(t, policyV1, "1.1.0")
	_, bearer := s.makeUserAndBearer(t, "anti3@test", []string{s.requiredRole}, false)
	// POST against v1 must 409 (status now 'superseded').
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, s.server.URL+"/v1/policies/"+policyV1+"/acknowledge",
		strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("Anti-3: want 409 on superseded ack, got %d body=%s", resp.StatusCode, raw)
	}
}

// ----- Time-injection unit-style test (clock cutoff math) -----

func TestDeriveAckToken_DayBuckets(t *testing.T) {
	user := uuid.NewString()
	version := uuid.NewString()
	t1 := time.Date(2026, 5, 13, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 13, 23, 59, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 14, 0, 1, 0, 0, time.UTC)
	a := policy.DeriveAckToken(user, version, t1)
	b := policy.DeriveAckToken(user, version, t2)
	c := policy.DeriveAckToken(user, version, t3)
	if a != b {
		t.Fatalf("same-day tokens differ: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("cross-day tokens equal: %s == %s", a, c)
	}
}
