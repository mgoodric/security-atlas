//go:build integration

// #1751: an ingest refusal on confirm reaches the operator as a 4xx with a
// stable code, not an opaque 500. Real Postgres, real router, real bearer
// auth, and the server wired with an ingest service the way cmd/atlas
// wires it.

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

func TestHTTP_ConfirmBackdatedPushEvidence_Returns400ObservedAtSkew(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin,
		"evidence_audit_log", "evidence_records", "controls", "mcp_write_proposals")
	ctx := context.Background()

	reg := schemaregistry.NewService(admin)
	if _, _, err := reg.ImportPlatformSchemas(ctx, schemaregistry.PlatformSchemasFS()); err != nil {
		t.Fatalf("ImportPlatformSchemas: %v", err)
	}
	srv := api.New(api.Config{RotationGrace: time.Hour, IngestService: ingest.New(app, reg)})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	approver := srv.IssueTestJWT(t, testjwt.ApproverFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	controlID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'MCP push target', 'GOV', 'manual_attested', $3)
	`, controlID, tenant, "legacy_"+controlID.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}

	observed := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	body := fmt.Sprintf(`{
		"tool_name": "push_evidence",
		"tool_input": {
			"control_id": %q,
			"kind": "manual.attestation.v1",
			"result": "pass",
			"observed_at": %q,
			"payload": {"attestor": "ops", "statement": "backdated"}
		},
		"ai_model_name": "m", "ai_model_version": "v"
	}`, controlID.String(), observed)
	resp, raw := doJSON(t, http.MethodPost, ts.URL+"/v1/mcp/write-proposals", bearer, body)
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
		ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/confirm", approver, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("confirm: status %d, want 400; body %s", resp.StatusCode, raw)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &errBody); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, raw)
	}
	if errBody.Code != "observed_at_skew" {
		t.Fatalf("code = %q, want observed_at_skew; body %s", errBody.Code, raw)
	}

	var rejected int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM evidence_audit_log
		WHERE tenant_id = $1 AND idempotency_key = $2 AND decision = 'rejected_observed_at_skew'
	`, tenant, "mcp-proposal:"+env.Proposal.ID).Scan(&rejected); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if rejected != 1 {
		t.Fatalf("rejected_observed_at_skew audit rows = %d, want 1", rejected)
	}
}
