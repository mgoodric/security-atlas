//go:build integration

// #2101 integration tests: who may approve a framework scope. Only a
// person approves, and never the person who submitted the scope, unless
// the organisation's Allow self-approval setting is on; then the scope is
// flagged self-approved and the workflow log's approve row carries the
// flag. A machine credential never approves. Driven through the full HTTP
// stack against real Postgres under RLS.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"fmt"
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

type selfApprovalHarness struct {
	t      *testing.T
	admin  *pgxpool.Pool
	tenant string
	fv     uuid.UUID
	srv    *api.Server
	ts     *httptest.Server
}

func newSelfApprovalHarness(t *testing.T) *selfApprovalHarness {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	fv := seedFrameworkVersion(t, admin, tenant)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(dbtest.NewAppPool(t))
	// Minting a token wires the test signer, which the handler must see
	// when it is built (the setupHTTPServer order).
	_ = srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return &selfApprovalHarness{t: t, admin: admin, tenant: tenant, fv: fv, srv: srv, ts: ts}
}

// person mints an approver JWT in the production subject shape.
func (h *selfApprovalHarness) person(user uuid.UUID) string {
	c := testjwt.ApproverFor(uuid.MustParse(h.tenant))
	c.Subject = "user:" + user.String()
	return h.srv.IssueTestJWT(h.t, c)
}

// machine mints an approver JWT for an OAuth client, which is no person.
func (h *selfApprovalHarness) machine() string {
	c := testjwt.ApproverFor(uuid.MustParse(h.tenant))
	c.Subject = "oauth_client:ci-" + uuid.NewString()[:8]
	return h.srv.IssueTestJWT(h.t, c)
}

func (h *selfApprovalHarness) call(method, path, bearer string, want int) map[string]any {
	h.t.Helper()
	resp, body := doJSON(h.t, method, h.ts.URL+path, bearer, "")
	if resp.StatusCode != want {
		h.t.Fatalf("%s %s: status %d (want %d) body=%s", method, path, resp.StatusCode, want, body)
	}
	var out struct {
		FrameworkScope map[string]any `json:"framework_scope"`
	}
	_ = json.Unmarshal(body, &out)
	return out.FrameworkScope
}

// submitted creates a scope and submits it as bearer, returning its id.
func (h *selfApprovalHarness) submitted(name, bearer string) string {
	h.t.Helper()
	body := fmt.Sprintf(`{"framework_version_id":%q,"name":%q,"predicate":{"op":"true"}}`, h.fv, name)
	resp, payload := doJSON(h.t, http.MethodPost, h.ts.URL+"/v1/framework-scopes", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("create: status %d body=%s", resp.StatusCode, payload)
	}
	var created struct {
		FrameworkScope struct {
			ID string `json:"id"`
		} `json:"framework_scope"`
	}
	if err := json.Unmarshal(payload, &created); err != nil {
		h.t.Fatalf("decode create: %v", err)
	}
	h.call(http.MethodPatch, "/v1/framework-scopes/"+created.FrameworkScope.ID+"/submit", bearer, http.StatusOK)
	return created.FrameworkScope.ID
}

func (h *selfApprovalHarness) allowSelfApproval(allow bool) {
	h.t.Helper()
	if _, err := h.admin.Exec(context.Background(), `
		INSERT INTO tenants (id, name, allow_self_approval) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET allow_self_approval = EXCLUDED.allow_self_approval`,
		h.tenant, "frameworkscope-2101-"+h.tenant[:8], allow); err != nil {
		h.t.Fatalf("set allow_self_approval: %v", err)
	}
	h.t.Cleanup(func() {
		_, _ = h.admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, h.tenant)
	})
}

func TestHTTP_Approve_SubmitterRefused_SecondApproverSucceeds(t *testing.T) {
	h := newSelfApprovalHarness(t)
	submitter := uuid.New()
	id := h.submitted("ISMS scope", h.person(submitter))
	path := "/v1/framework-scopes/" + id + "/approve"

	// The submitter, from a second session, is refused.
	resp, body := doJSON(t, http.MethodPatch, h.ts.URL+path, h.person(submitter), "")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "cannot approve their own scope") {
		t.Fatalf("self-approval: status %d (want 403) body=%s", resp.StatusCode, body)
	}
	// A machine credential is refused.
	resp, body = doJSON(t, http.MethodPatch, h.ts.URL+path, h.machine(), "")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "only a person can approve") {
		t.Fatalf("machine approval: status %d (want 403) body=%s", resp.StatusCode, body)
	}

	// A second person approves; the scope is not flagged.
	second := uuid.New()
	approved := h.call(http.MethodPatch, path, h.person(second), http.StatusOK)
	if approved["state"] != "approved" || approved["approver_user_id"] != second.String() || approved["self_approved"] != false {
		t.Fatalf("second approver: %v, want approved by %s and unflagged", approved, second)
	}
	if approved["submitted_by"] != submitter.String() {
		t.Fatalf("submitted_by = %v, want the bare user id %s", approved["submitted_by"], submitter)
	}
}

func TestHTTP_Approve_SelfApprovalOptIn(t *testing.T) {
	h := newSelfApprovalHarness(t)
	h.allowSelfApproval(true)
	submitter := uuid.New()

	// Even with self-approval on, a machine credential never approves.
	machineScope := h.submitted("Machine scope", h.person(submitter))
	resp, body := doJSON(t, http.MethodPatch, h.ts.URL+"/v1/framework-scopes/"+machineScope+"/approve", h.machine(), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("machine approval with self-approval on: status %d (want 403) body=%s", resp.StatusCode, body)
	}

	id := h.submitted("Self approved scope", h.person(submitter))
	approved := h.call(http.MethodPatch, "/v1/framework-scopes/"+id+"/approve", h.person(submitter), http.StatusOK)
	if approved["state"] != "approved" || approved["approver_user_id"] != submitter.String() || approved["self_approved"] != true {
		t.Fatalf("self-approval: %v, want approved by the submitter and flagged", approved)
	}

	// The workflow log's approve row carries the flag in its after-image.
	var after []byte
	if err := h.admin.QueryRow(context.Background(), `
		SELECT after FROM framework_scope_workflow_log
		 WHERE tenant_id = $1 AND framework_scope_id = $2 AND action = 'approve'`,
		h.tenant, id).Scan(&after); err != nil {
		t.Fatalf("read workflow log: %v", err)
	}
	var image map[string]any
	if err := json.Unmarshal(after, &image); err != nil {
		t.Fatalf("decode after-image: %v", err)
	}
	if image["self_approved"] != true {
		t.Fatalf("approve row after-image self_approved = %v, want true (image %s)", image["self_approved"], after)
	}

	// Editing the predicate bounces the scope to draft and clears the flag.
	resp, body = doJSON(t, http.MethodPatch, h.ts.URL+"/v1/framework-scopes/"+id, h.person(submitter), `{"predicate":{"op":"eq","dim":"environment","value":"prod"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit predicate: status %d body=%s", resp.StatusCode, body)
	}
	got := h.call(http.MethodGet, "/v1/framework-scopes/"+id, h.person(submitter), http.StatusOK)
	if got["state"] != "draft" || got["self_approved"] != false {
		t.Fatalf("after predicate edit: %v, want draft and unflagged", got)
	}
}
