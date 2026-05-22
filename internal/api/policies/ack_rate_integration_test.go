//go:build integration

// Slice 107 integration tests — `GET /v1/policies?include=ack_rate` join.
//
// These tests exercise:
//   * ISC-11 — the omitted-`?include` shape is unchanged (additive).
//   * ISC-13 — a tenant with only draft policies returns `ack_rate: null`
//     on every row (the SQL CASE WHEN status='published' branch).
//   * ISC-18 — RLS round-trip: Tenant A's request never sees Tenant B's
//     ack rows.
//   * ISC-19 — non-published policies in the same tenant return
//     `ack_rate: null` alongside published rows that carry a populated
//     cell.
//   * ISC-20 — published policy with one fresh ack reports numerator=1,
//     denominator>=1.
//
// Setup mirrors slice 023's policyacks integration harness: real
// Postgres, real schema registry, real ingest service. The tests then
// drive the full create -> approve -> publish -> acknowledge chain via
// HTTP so the joined ack_rate query exercises the same rows the
// per-policy /v1/policies/{id}/acknowledgment-rate handler reads.
//
// Constitutional anti-criteria honored:
//   - ISC-A1: the handler runs ONE query (verified by the existence of
//     dbx.ListPoliciesWithAckRate and the no-loop wire layer the unit
//     tests pin).
//   - ISC-A2: the omitted-include shape MUST stay the v1 shape.
//   - ISC-A3: tenant isolation must hold under the joined query (RLS,
//     not application code).
//   - ISC-A4: the numerator/denominator math MUST match the per-policy
//     handler's output (we cross-check via a round-trip comparison).

package policies_test

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
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// ----- env helpers (mirrors slice 023 harness) -----

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

// freshTenant returns a new random tenant id and registers cleanup for
// every table the slice-107 path touches. Mirrors slice 023's pattern.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
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

// seedSCFAnchor mirrors slice 023's helper so the schema registry's
// GOV-04 default_scf_anchors validation passes for policy.acknowledgment.v1.
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
// slice 023's helper.
func bootRegistry(t *testing.T, admin *pgxpool.Pool) *schemaregistry.Service {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin conn: %v", err)
	}
	defer conn.Release()
	_, _ = conn.Exec(ctx, "SELECT pg_advisory_lock(6502261335191781149)")
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(6502261335191781149)")
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
				 $6::jsonb, $7, $8, 'slice-107-test-bootstrap')
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

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "legacy-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'Test control', 'IAC', 'automated', $3)
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

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

func seedAPIKeyForUser(t *testing.T, admin *pgxpool.Pool, tenant string, userID uuid.UUID, isAdmin bool, ownerRoles []string) {
	t.Helper()
	if ownerRoles == nil {
		ownerRoles = []string{}
	}
	hash := uuid.New().String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO api_keys (id, tenant_id, token_hash, issued_by, is_admin, is_approver, owner_roles, last4)
		VALUES (gen_random_uuid(), $1, decode($2, 'hex'), $3, $4, false, $5, '0000')
	`, tenant, fmt.Sprintf("%064x", []byte(hash)[:32]), userID, isAdmin, ownerRoles); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
}

// ----- harness -----

type setupResult struct {
	server    *httptest.Server
	app       *pgxpool.Pool
	admin     *pgxpool.Pool
	apiServer *api.Server
	ingester  *ingest.Service
	tenant    string
	ctrlID    uuid.UUID
	adminBear string
	adminUser uuid.UUID
}

func setup(t *testing.T) setupResult {
	t.Helper()
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	reg := bootRegistry(t, admin)
	seedSCFAnchor(t, admin, "GOV-04", "GOV")
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	ingester := ingest.New(app, reg)
	apiServer := api.New(api.Config{
		RotationGrace:  time.Hour,
		SchemaRegistry: reg,
		IngestService:  ingester,
	})
	apiServer.AttachDB(app)

	// Slice 197: JWT bearer via slice 190 path; Subject = admin user
	// id so policy_acknowledgments FK targets resolve.
	adminUser := seedUser(t, admin, tenant, "admin@slice107.test")
	seedAPIKeyForUser(t, admin, tenant, adminUser, true, nil)
	adminClaims := testjwt.AdminFor(uuid.MustParse(tenant))
	adminClaims.Subject = adminUser.String()
	adminBearer := apiServer.IssueTestJWT(t, adminClaims)

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
		server:    ts,
		app:       app,
		admin:     admin,
		apiServer: apiServer,
		ingester:  ingester,
		tenant:    tenant,
		ctrlID:    ctrlID,
		adminBear: adminBearer,
		adminUser: adminUser,
	}
}

func (s setupResult) do(t *testing.T, method, path string, body []byte, bearer string, wantStatus int) []byte {
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
	return raw
}

// createDraftPolicy creates a draft via POST /v1/policies and returns
// the new policy id.
func createDraftPolicy(t *testing.T, s setupResult, title, version string, requiredRoles []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"title":                         title,
		"version":                       version,
		"body_md":                       "# " + title,
		"owner_role":                    "tenant_admin",
		"approver_role":                 "security_lead",
		"linked_control_ids":            []string{s.ctrlID.String()},
		"acknowledgment_required_roles": requiredRoles,
		"source_attribution":            "tenant_authored",
	})
	raw := s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, http.StatusCreated)
	var env struct {
		Policy struct {
			ID string `json:"id"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal create: %v body=%s", err, raw)
	}
	return env.Policy.ID
}

func createAndPublishPolicy(t *testing.T, s setupResult, title, version string, requiredRoles []string) string {
	t.Helper()
	id := createDraftPolicy(t, s, title, version, requiredRoles)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.adminBear, http.StatusOK)
	pubBody, _ := json.Marshal(map[string]any{"new_version": version})
	raw := s.do(t, http.MethodPost, "/v1/policies/"+id+"/publish", pubBody, s.adminBear, http.StatusCreated)
	var env struct {
		Policy struct {
			ID string `json:"id"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal publish: %v body=%s", err, raw)
	}
	if env.Policy.ID != "" {
		return env.Policy.ID
	}
	return id
}

// ----- tests -----

// ISC-11 — omitted `?include` returns the existing shape (no ack_rate
// key on the row). Additive guarantee — existing callers cannot break.
func TestListPolicies_OmittedIncludeReturnsV1Shape(t *testing.T) {
	s := setup(t)
	_ = createAndPublishPolicy(t, s, "Pinned-Shape", "1", []string{"all_staff"})

	raw := s.do(t, http.MethodGet, "/v1/policies", nil, s.adminBear, http.StatusOK)
	var got struct {
		Policies []map[string]any `json:"policies"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}
	if len(got.Policies) == 0 {
		t.Fatal("expected at least one policy")
	}
	if _, has := got.Policies[0]["ack_rate"]; has {
		t.Errorf("omitted ?include should NOT include ack_rate key; got %+v", got.Policies[0])
	}
}

// ISC-13 + ISC-19 — a tenant with only draft policies returns
// `ack_rate: null` on every row. The SQL CASE WHEN status='published'
// branch did not fire.
func TestListPolicies_IncludeAckRate_DraftOnly_AllNull(t *testing.T) {
	s := setup(t)
	_ = createDraftPolicy(t, s, "Draft-1", "1", []string{"all_staff"})
	_ = createDraftPolicy(t, s, "Draft-2", "2", []string{"engineering"})

	raw := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, s.adminBear, http.StatusOK)
	var got struct {
		Policies []map[string]any `json:"policies"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}
	if len(got.Policies) < 2 {
		t.Fatalf("expected at least 2 policies; got %d", len(got.Policies))
	}
	for i, p := range got.Policies {
		ackRate, has := p["ack_rate"]
		if !has {
			t.Errorf("policy[%d] missing ack_rate key: %+v", i, p)
			continue
		}
		if ackRate != nil {
			t.Errorf("policy[%d] (status=%v) should have ack_rate: null; got %+v", i, p["status"], ackRate)
		}
	}
}

// ISC-20 — published policy with at least one fresh ack reports
// numerator>=1, denominator>=1. The freshness window applies (cutoff
// in the SQL); a fresh ack made just now counts.
func TestListPolicies_IncludeAckRate_PublishedWithAck_PopulatedCell(t *testing.T) {
	s := setup(t)
	requiredRole := "employee"

	// Publish a policy that requires the `employee` role.
	policyID := createAndPublishPolicy(t, s, "Acked-Policy", "1", []string{requiredRole})

	// Mint a user + api_key with the required role, then acknowledge.
	userID, bearer := s.issueRoledUserBearer(t, "alice@slice107.test", []string{requiredRole})
	_ = userID
	s.do(t, http.MethodPost, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)

	// Now hit the joined list and find that row.
	raw := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, s.adminBear, http.StatusOK)
	var got struct {
		Policies []struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Title   string `json:"title"`
			AckRate *struct {
				Numerator   int64    `json:"numerator"`
				Denominator int64    `json:"denominator"`
				Percent     *float64 `json:"percent"`
			} `json:"ack_rate"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}
	var hit bool
	for _, p := range got.Policies {
		if p.ID != policyID {
			continue
		}
		hit = true
		if p.Status != "published" {
			t.Errorf("expected published; got %q", p.Status)
		}
		if p.AckRate == nil {
			t.Fatalf("expected populated ack_rate cell for published row %s; got nil", policyID)
		}
		if p.AckRate.Numerator < 1 {
			t.Errorf("expected numerator >= 1 (alice acked); got %d", p.AckRate.Numerator)
		}
		if p.AckRate.Denominator < 1 {
			t.Errorf("expected denominator >= 1 (alice has the role); got %d", p.AckRate.Denominator)
		}
	}
	if !hit {
		t.Fatalf("policy %s not found in joined list", policyID)
	}
}

// ISC-18 — RLS round-trip. Tenant A publishes + acks a policy; Tenant
// B's bearer hits the SAME joined endpoint and MUST NOT see any of
// Tenant A's rows.
func TestListPolicies_IncludeAckRate_RLS_TenantIsolation(t *testing.T) {
	s := setup(t)
	requiredRole := "employee"

	// Tenant A: publish a policy + ack it.
	policyA := createAndPublishPolicy(t, s, "TenantA-Policy", "1", []string{requiredRole})
	_, bearerA := s.issueRoledUserBearer(t, "tenantA-user@slice107.test", []string{requiredRole})
	s.do(t, http.MethodPost, "/v1/policies/"+policyA+"/acknowledge", []byte("{}"), bearerA, http.StatusCreated)

	// Sanity: tenant A bearer sees its row in the joined list.
	rawA := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, s.adminBear, http.StatusOK)
	if !strings.Contains(string(rawA), policyA) {
		t.Fatalf("tenant A should see its own policy in the joined list; body=%s", rawA)
	}

	// Tenant B: fresh tenant on the same server. Issue an admin bearer
	// for tenant B; the joined list should be EMPTY (or at least not
	// contain policyA).
	tenantB := freshTenant(t, s.admin)
	bearerB := s.apiServer.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenantB)))
	rawB := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, bearerB, http.StatusOK)
	if strings.Contains(string(rawB), policyA) {
		t.Fatalf("RLS BYPASS: tenant B's joined list contains tenant A's policy %s; body=%s", policyA, rawB)
	}
	// Tenant B has no policies, so the list is empty (length 0).
	var got struct {
		Policies []map[string]any `json:"policies"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(rawB, &got); err != nil {
		t.Fatalf("unmarshal tenant B: %v body=%s", err, rawB)
	}
	if len(got.Policies) != 0 {
		t.Errorf("tenant B should see 0 policies; got %d (%+v)", len(got.Policies), got.Policies)
	}
}

// ISC-A4 — the joined query must produce the SAME numerator/denominator
// the per-policy GET /v1/policies/{id}/acknowledgment-rate returns. This
// is the canonical anti-criterion check: the same predicates, the same
// math.
func TestListPolicies_IncludeAckRate_MatchesPerPolicyHandler(t *testing.T) {
	s := setup(t)
	requiredRole := "employee"

	policyID := createAndPublishPolicy(t, s, "Cross-Check", "1", []string{requiredRole})
	_, bearer := s.issueRoledUserBearer(t, "bob@slice107.test", []string{requiredRole})
	s.do(t, http.MethodPost, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)

	// Per-policy rate (the canonical path).
	rawPer := s.do(t, http.MethodGet, "/v1/policies/"+policyID+"/acknowledgment-rate", nil, s.adminBear, http.StatusOK)
	var per struct {
		Numerator   int64    `json:"numerator"`
		Denominator int64    `json:"denominator"`
		Percent     *float64 `json:"percent"`
	}
	if err := json.Unmarshal(rawPer, &per); err != nil {
		t.Fatalf("unmarshal per-policy: %v body=%s", err, rawPer)
	}

	// Joined list rate.
	rawList := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, s.adminBear, http.StatusOK)
	var list struct {
		Policies []struct {
			ID      string `json:"id"`
			AckRate *struct {
				Numerator   int64    `json:"numerator"`
				Denominator int64    `json:"denominator"`
				Percent     *float64 `json:"percent"`
			} `json:"ack_rate"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(rawList, &list); err != nil {
		t.Fatalf("unmarshal list: %v body=%s", err, rawList)
	}
	var joined *struct {
		Numerator   int64    `json:"numerator"`
		Denominator int64    `json:"denominator"`
		Percent     *float64 `json:"percent"`
	}
	for _, p := range list.Policies {
		if p.ID == policyID {
			joined = p.AckRate
		}
	}
	if joined == nil {
		t.Fatalf("joined list missing ack_rate for %s", policyID)
	}
	if joined.Numerator != per.Numerator {
		t.Errorf("numerator drift: joined=%d per-policy=%d", joined.Numerator, per.Numerator)
	}
	if joined.Denominator != per.Denominator {
		t.Errorf("denominator drift: joined=%d per-policy=%d", joined.Denominator, per.Denominator)
	}
	if (joined.Percent == nil) != (per.Percent == nil) {
		t.Errorf("percent null-state drift: joined=%v per-policy=%v", joined.Percent, per.Percent)
	}
	if joined.Percent != nil && per.Percent != nil && *joined.Percent != *per.Percent {
		t.Errorf("percent value drift: joined=%f per-policy=%f", *joined.Percent, *per.Percent)
	}
}

// TestListPolicies_IncludeAckRate_WireShape_PinsNullVsNumber is the
// slice 159 belt-and-suspenders test (AC-6). The slice 159 codegen
// changes shifted `AckDenominator` / `AckNumerator` from
// `pgtype.Int8` (with a `.Valid` field) to `*int64` (nil = NULL).
// This test asserts the JSON-on-the-wire shape — that the keys are
// present, that nil pointers serialize as JSON `null`, and that
// populated pointers serialize as JSON numbers. Without this test
// pin, a future codegen-type drift could silently change the wire
// contract (e.g. emit `*int64` -> nil as `{}` rather than `null` if
// the struct tags drift). The earlier tests assert numeric values
// but not the marshal semantics of the null branch.
func TestListPolicies_IncludeAckRate_WireShape_PinsNullVsNumber(t *testing.T) {
	s := setup(t)

	// One draft policy: ack_rate columns MUST be JSON null.
	draftID := createDraftPolicy(t, s, "Draft-Policy-WireShape", "1", []string{"employee"})

	// One published-with-no-acks policy: ack_rate cell MUST be populated
	// with denominator >= 0 (the API key the bootstrap-admin holds may
	// or may not match the required role; either way, both columns
	// MUST be JSON numbers, not null).
	publishedID := createAndPublishPolicy(t, s, "Published-No-Ack-WireShape", "1", []string{"employee"})

	raw := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, s.adminBear, http.StatusOK)

	// Decode to a shape that tells us whether the key was PRESENT in
	// the wire and whether the value was a JSON number vs JSON null.
	// Using json.RawMessage lets us inspect the literal bytes.
	var envelope struct {
		Policies []struct {
			ID      string          `json:"id"`
			Status  string          `json:"status"`
			AckRate json.RawMessage `json:"ack_rate"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}

	var sawDraft, sawPublished bool
	for _, p := range envelope.Policies {
		switch p.ID {
		case draftID:
			sawDraft = true
			// Draft → ack_rate MUST be the JSON literal `null` (key present, value null).
			if string(p.AckRate) != "null" {
				t.Errorf("draft policy %s: ack_rate = %q; want literal `null`",
					p.ID, string(p.AckRate))
			}
		case publishedID:
			sawPublished = true
			// Published → ack_rate MUST be a JSON object with `numerator`
			// + `denominator` as JSON numbers (not nulls). We don't
			// assert the values here — the OTHER tests do that. We
			// assert the SHAPE: ack_rate is non-null, denominator is a
			// JSON number, numerator is a JSON number.
			if string(p.AckRate) == "null" {
				t.Errorf("published policy %s: ack_rate = `null`; want populated cell",
					p.ID)
				continue
			}
			var cell struct {
				Numerator   *int64 `json:"numerator"`
				Denominator *int64 `json:"denominator"`
			}
			if err := json.Unmarshal(p.AckRate, &cell); err != nil {
				t.Errorf("published policy %s: ack_rate body %q does not unmarshal: %v",
					p.ID, string(p.AckRate), err)
				continue
			}
			if cell.Numerator == nil {
				t.Errorf("published policy %s: numerator is JSON null; want a number",
					p.ID)
			}
			if cell.Denominator == nil {
				t.Errorf("published policy %s: denominator is JSON null; want a number",
					p.ID)
			}
		}
	}
	if !sawDraft {
		t.Fatalf("draft policy %s not found in joined list", draftID)
	}
	if !sawPublished {
		t.Fatalf("published policy %s not found in joined list", publishedID)
	}
}

// issueRoledUserBearer mints a non-admin owner bearer carrying the
// supplied roles, then seeds a users row + an api_keys row so the
// joined query's denominator (api_keys) counts this user. The bearer's
// UserID is rebound to the users row id so the acknowledgment FK
// (policy_acknowledgments.user_id -> users.id) is satisfied.
func (s *setupResult) issueRoledUserBearer(t *testing.T, email string, roles []string) (uuid.UUID, string) {
	t.Helper()
	userID := seedUser(t, s.admin, s.tenant, email)
	seedAPIKeyForUser(t, s.admin, s.tenant, userID, false, roles)
	// Slice 197: JWT bearer with Subject = users row id (replaces
	// the legacy RebindBearerUserIDForTests hook).
	claims := testjwt.OwnerFor(uuid.MustParse(s.tenant), roles)
	claims.Subject = userID.String()
	return userID, s.apiServer.IssueTestJWT(t, claims)
}
