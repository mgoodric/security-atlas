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
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
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
	approverID     uuid.UUID
	viewerBearer   string
	grcBearer      string
}

func setupHTTPServer(t *testing.T, tenant string) harness {
	t.Helper()
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	// #3016: seeded people with live user_roles grants, on the real
	// engine. Only admin decides a proposal; grc_engineer may file one
	// (#3031), viewer neither. The proposer and the approver here are two
	// different admins.
	bearer := liveroletest.Bearer(t, srv, tenant, "admin")
	approverID, approver := liveroletest.Person(t, srv, tenant, "admin")
	viewer := liveroletest.Bearer(t, srv, tenant, "viewer")
	grc := liveroletest.Bearer(t, srv, tenant, "grc_engineer")
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return harness{ts: ts, bearer: bearer, approverBearer: approver, approverID: approverID, viewerBearer: viewer, grcBearer: grc}
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

// #3016: the engine refuses a viewer both the create and the confirm,
// with its own {"error":"forbidden"}, and the proposal stays
// ai_proposed. The handlers' IsTenantAdmin gate ("admin role required")
// is behind it: the engine admits only a live admin to confirm and
// reject, and that grant is the one the gate reads. grc_engineer's create
// admit is TestHTTP_GRCEngineerFilesAdminDecides (#3031).
func TestHTTP_ConfirmRequiresApprover(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "T", "category": "c"},
		"ai_model_name": "m", "ai_model_version": "v"
	}`
	if resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.viewerBearer, body); resp.StatusCode != http.StatusForbidden || !isEngineForbidden(raw) {
		t.Fatalf("viewer create: %d %s; want 403 forbidden", resp.StatusCode, raw)
	}

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
		h.viewerBearer, "")
	if resp.StatusCode != http.StatusForbidden || !isEngineForbidden(raw) {
		t.Fatalf("viewer confirm: %d %s; want 403 forbidden", resp.StatusCode, raw)
	}

	var state string
	var proposals int
	if err := admin.QueryRow(context.Background(),
		`SELECT (SELECT state FROM mcp_write_proposals WHERE id = $1),
		        (SELECT count(*) FROM mcp_write_proposals WHERE tenant_id = $2)`,
		env.Proposal.ID, tenant).Scan(&state, &proposals); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if state != "ai_proposed" || proposals != 1 {
		t.Fatalf("after the viewer's refusals: state %q, %d proposals; want ai_proposed, 1", state, proposals)
	}
}

// #3031 (decided on #3022): a live grc_engineer files a proposal (201)
// and the engine refuses it confirm and reject with its own
// {"error":"forbidden"}, before the handler's IsTenantAdmin gate ("admin
// role required"), which TestDecideProposal_403ForGRCEngineerAndApproverFlag
// pins on its own. An admin then confirms the grc_engineer's proposal.
func TestHTTP_GRCEngineerFilesAdminDecides(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)

	body := `{
		"tool_name": "create_risk",
		"tool_input": {"title": "Filed by grc_engineer", "category": "operational"},
		"ai_model_name": "m", "ai_model_version": "v"
	}`
	resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.grcBearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("grc_engineer create: %d %s; want 201", resp.StatusCode, raw)
	}
	var env struct {
		Proposal struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Proposal.ID == "" {
		t.Fatalf("create body %s: %v", raw, err)
	}

	for _, verb := range []string{"confirm", "reject"} {
		resp, raw = doJSON(t, http.MethodPost,
			h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/"+verb, h.grcBearer, "")
		if resp.StatusCode != http.StatusForbidden || !isEngineForbidden(raw) {
			t.Fatalf("grc_engineer %s: %d %s; want the engine's 403 forbidden", verb, resp.StatusCode, raw)
		}
	}
	var state string
	if err := admin.QueryRow(context.Background(),
		`SELECT state FROM mcp_write_proposals WHERE id = $1`, env.Proposal.ID).Scan(&state); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if state != "ai_proposed" {
		t.Fatalf("after the grc_engineer's refusals: state %q, want ai_proposed", state)
	}

	resp, raw = doJSON(t, http.MethodPost,
		h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/confirm", h.approverBearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin confirm: %d %s; want 200", resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Proposal.State != "applied" {
		t.Fatalf("admin confirm: body %s, want state applied (%v)", raw, err)
	}
}

// isEngineForbidden reports whether raw is the authz middleware's own 403
// body, which no handler gate returns.
func isEngineForbidden(raw []byte) bool {
	var body map[string]any
	return json.Unmarshal(raw, &body) == nil && body["error"] == "forbidden"
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
