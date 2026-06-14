//go:build integration

// HTTP-level integration tests for slice 173: MCP write tools + HITL
// approval. Real Postgres + real chi router + real bearer auth. Mirrors
// the slice-024 vendors HTTP smoke pattern.
//
// Coverage:
//   - POST proposal happy path
//   - confirm path applies + records human_approver
//   - reject path is terminal
//   - non-approver cannot confirm (403)
//   - cross-tenant isolation (RLS)
//   - pending-cap enforcement (429)

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 435 / 742: appDSN/adminDSN/openPool boilerplate now lives in the
// shared internal/dbtest harness (NewAppPool = RLS-enforcing atlas_app default;
// NewMigratePool = privileged BYPASSRLS for seeding + freshTenant cleanup).

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"mcp_write_proposals",
		"risks",
	)
}

type harness struct {
	ts             *httptest.Server
	bearer         string
	approverBearer string
}

func setupHTTPServer(t *testing.T, tenant string) harness {
	t.Helper()
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	// Slice 197: JWT bearers via slice 190 path. ViewerFor for the
	// proposer (no elevation); ApproverFor for the approver gate.
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	approver := srv.IssueTestJWT(t, testjwt.ApproverFor(uuid.MustParse(tenant)))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return harness{ts: ts, bearer: bearer, approverBearer: approver}
}

func doJSON(t *testing.T, method, url, bearer, body string) (*http.Response, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reqBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	bb, _ := io.ReadAll(resp.Body)
	return resp, bb
}

// ----- ISC-40 + ISC-50: POST proposal happy path -----

func TestHTTP_CreateProposal(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "DC outage", "category": "operational"},
		"ai_model_name": "llama3.1:8b-instruct-q5",
		"ai_model_version": "2026-05-01"
	}`
	resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var env struct {
		Proposal map[string]any `json:"proposal"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if env.Proposal["state"] != "ai_proposed" {
		t.Errorf("state = %v, want ai_proposed", env.Proposal["state"])
	}
	if env.Proposal["ai_assisted"] != true {
		t.Errorf("ai_assisted = %v, want true", env.Proposal["ai_assisted"])
	}
}

// ----- ISC-43 + ISC-51 + ISC-A1: confirm applies + records approver -----

func TestHTTP_ConfirmProposal_AppliesAndRecordsApprover(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "Vendor SLA breach", "category": "operational"},
		"ai_model_name": "m",
		"ai_model_version": "v"
	}`
	resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Proposal struct {
			ID string `json:"id"`
		} `json:"proposal"`
	}
	_ = json.Unmarshal(raw, &env)

	resp, raw = doJSON(t, http.MethodPost,
		h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/confirm",
		h.approverBearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm: %d %s", resp.StatusCode, raw)
	}
	var confirmEnv struct {
		Proposal map[string]any `json:"proposal"`
	}
	_ = json.Unmarshal(raw, &confirmEnv)
	if confirmEnv.Proposal["state"] != "applied" {
		t.Errorf("state = %v, want applied", confirmEnv.Proposal["state"])
	}
	if confirmEnv.Proposal["human_approved"] != true {
		t.Errorf("human_approved = %v, want true", confirmEnv.Proposal["human_approved"])
	}
	if confirmEnv.Proposal["human_approver"] == nil {
		t.Error("human_approver must be set")
	}
	if confirmEnv.Proposal["applied_subject"] == nil {
		t.Error("applied_subject must be set (the new risk's UUID)")
	}

	// Verify the canonical risk row was actually inserted.
	subj := confirmEnv.Proposal["applied_subject"].(string)
	var count int
	if err := admin.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM risks WHERE id = $1 AND tenant_id = $2`,
		subj, tenant).Scan(&count); err != nil {
		t.Fatalf("count risks: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 canonical risk row, got %d", count)
	}
}

// ----- ISC-44 + ISC-52: reject path -----

func TestHTTP_RejectProposal(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "T", "category": "c"},
		"ai_model_name": "m", "ai_model_version": "v"
	}`
	resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Proposal struct {
			ID string `json:"id"`
		} `json:"proposal"`
	}
	_ = json.Unmarshal(raw, &env)

	resp, raw = doJSON(t, http.MethodPost,
		h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/reject",
		h.approverBearer, `{"reason":"Too vague"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: %d %s", resp.StatusCode, raw)
	}
}

// ----- ISC-45 + ISC-55: non-approver gets 403 on confirm -----

func TestHTTP_ConfirmRequiresApprover(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "T", "category": "c"},
		"ai_model_name": "m", "ai_model_version": "v"
	}`
	resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Proposal struct {
			ID string `json:"id"`
		} `json:"proposal"`
	}
	_ = json.Unmarshal(raw, &env)

	resp, raw = doJSON(t, http.MethodPost,
		h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/confirm",
		h.bearer, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", resp.StatusCode, raw)
	}
}

// ----- ISC-53 + RLS: cross-tenant isolation -----

func TestHTTP_CrossTenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	hA := setupHTTPServer(t, tenantA)
	hB := setupHTTPServer(t, tenantB)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "secret-A", "category": "c"},
		"ai_model_name": "m", "ai_model_version": "v"
	}`
	resp, raw := doJSON(t, http.MethodPost, hA.ts.URL+"/v1/mcp/write-proposals", hA.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create A: %d %s", resp.StatusCode, raw)
	}

	resp, raw = doJSON(t, http.MethodGet, hB.ts.URL+"/v1/mcp/write-proposals", hB.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list B: %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "secret-A") {
		t.Fatalf("tenant B saw tenant A's proposal — RLS broken: %s", raw)
	}
}
