//go:build integration

// #2108 — verifying an action plan by hand. An approver verifies a
// completed plan with no linked controls through POST
// /v1/action-plans/{id}/verify; the plan's owner never can, even with Allow
// self-approval on; a credential that is not a person is refused; a plan
// with linked controls is refused (its controls' evaluations verify it);
// and PATCH no longer sets verified. The HTTP tests run the assembled
// router against real Postgres under RLS.
//
// Run with:
//
//	go test -tags=integration ./internal/actionplan/ -run Verify

package actionplan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// verifyEnv is a server with two people in one tenant, both tenant admins
// by live role: the plan's owner and a second approver. Because both hold
// the role, the owner rule, not the role check, is what refuses the owner.
// The server resolves roles from user_roles, as production does (#2507).
type verifyEnv struct {
	srv      *api.Server
	admin    *pgxpool.Pool
	store    *actionplan.Store
	tenant   string
	server   *httptest.Server
	owner    uuid.UUID
	approver uuid.UUID
	ownerJWT string
	apprJWT  string
	// nonApprJWT is a real person holding the live grc_engineer role and
	// no approver flag (#2434). grc_engineer approves (#2428) but does not
	// write action plans, and verify is a write: the role gate refuses it.
	nonApprJWT string
	// machineJWT is an approver credential that belongs to no user.
	machineJWT string
}

func newVerifyEnv(t *testing.T) verifyEnv {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	env := verifyEnv{admin: admin, store: actionplan.NewStore(app), tenant: tenant, srv: srv}
	env.owner = env.seedAdmin(t)
	env.approver = env.seedAdmin(t)
	env.ownerJWT, env.apprJWT = env.jwtFor(t, env.owner), env.jwtFor(t, env.approver)
	env.nonApprJWT = liveroletest.Bearer(t, srv, tenant, "grc_engineer")
	tid := uuid.MustParse(tenant)
	env.machineJWT = srv.IssueTestJWT(t, testjwt.AdminFor(tid))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil")
	}
	env.server = httptest.NewServer(h)
	t.Cleanup(env.server.Close)
	return env
}

// seedAdmin seeds a user of the env's tenant holding the live admin role.
func (env verifyEnv) seedAdmin(t *testing.T) uuid.UUID {
	t.Helper()
	u := seedUser(t, env.admin, env.tenant)
	if _, err := env.admin.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'actionplan-2434')`,
		env.tenant, u.String()); err != nil {
		t.Fatalf("seed admin role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.admin.Exec(context.Background(),
			`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, env.tenant, u.String())
	})
	return u
}

// jwtFor is a sign-in JWT for user u of the env's tenant.
func (env verifyEnv) jwtFor(t *testing.T, u uuid.UUID) string {
	t.Helper()
	claims := testjwt.AdminFor(uuid.MustParse(env.tenant))
	claims.Subject = "user:" + u.String()
	return env.srv.IssueTestJWT(t, claims)
}

func (env verifyEnv) do(t *testing.T, method, path, bearer string, payload any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, _ := http.NewRequest(method, env.server.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// completedPlan creates a plan owned by env.owner and moves it to
// completed through the API, as the stepper does.
func (env verifyEnv) completedPlan(t *testing.T) string {
	t.Helper()
	code, body := env.do(t, http.MethodPost, "/v1/action-plans", env.ownerJWT, map[string]any{
		"title":    "Verify-by-hand plan",
		"owner_id": env.owner.String(),
	})
	if code != http.StatusCreated {
		t.Fatalf("create: status %d: %v", code, body)
	}
	id := body["action_plan"].(map[string]any)["id"].(string)
	for _, s := range []string{actionplan.StatusInProgress, actionplan.StatusCompleted} {
		if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, env.ownerJWT, map[string]any{"status": s}); code != http.StatusOK {
			t.Fatalf("PATCH status %s: %d: %v", s, code, body)
		}
	}
	return id
}

func (env verifyEnv) status(t *testing.T, id string) string {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/v1/action-plans/"+id, env.apprJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("GET: %d: %v", code, body)
	}
	return body["action_plan"].(map[string]any)["status"].(string)
}

func (env verifyEnv) setAllowSelfApproval(t *testing.T, allow bool) {
	t.Helper()
	tag, err := env.admin.Exec(context.Background(),
		`UPDATE tenants SET allow_self_approval = $2 WHERE id = $1`, env.tenant, allow)
	if err != nil {
		t.Fatalf("set allow_self_approval: %v", err)
	}
	if tag.RowsAffected() > 0 {
		return
	}
	if _, err := env.admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name, allow_self_approval) VALUES ($1, $2, $3)`,
		env.tenant, "actionplan-2108-"+env.tenant[:8], allow); err != nil {
		t.Fatalf("insert tenants row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, env.tenant)
	})
}

// TestVerify_OwnerCannotApproverCan is the ticket's API check: the owner of
// a no-control plan can't verify it; an approver can. The verify writes one
// status_changed audit row whose actor is the approver.
func TestVerify_OwnerCannotApproverCan(t *testing.T) {
	env := newVerifyEnv(t)
	id := env.completedPlan(t)

	code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.ownerJWT, nil)
	if code != http.StatusForbidden {
		t.Fatalf("owner verify: status %d, want 403: %v", code, body)
	}
	if got := env.status(t, id); got != actionplan.StatusCompleted {
		t.Fatalf("after owner's refused verify: status %q, want completed", got)
	}

	code, body = env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.apprJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("approver verify: status %d, want 200: %v", code, body)
	}
	if got := body["action_plan"].(map[string]any)["status"]; got != actionplan.StatusVerified {
		t.Fatalf("approver verify: status %v, want verified", got)
	}

	var actor uuid.UUID
	if err := env.admin.QueryRow(context.Background(), `
		SELECT actor_id FROM action_plan_audit_log
		WHERE tenant_id = $1 AND action_plan_id = $2 AND after_state->>'status' = 'verified'`,
		env.tenant, id).Scan(&actor); err != nil {
		t.Fatalf("verify audit row: %v", err)
	}
	if actor != env.approver {
		t.Fatalf("verify audit actor = %s, want the approver %s", actor, env.approver)
	}
}

// TestVerify_OwnerRefusedEvenWithSelfApproval: Allow self-approval does not
// let the owner verify their own plan.
func TestVerify_OwnerRefusedEvenWithSelfApproval(t *testing.T) {
	env := newVerifyEnv(t)
	env.setAllowSelfApproval(t, true)
	id := env.completedPlan(t)
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.ownerJWT, nil); code != http.StatusForbidden {
		t.Fatalf("owner verify with self-approval on: status %d, want 403: %v", code, body)
	}
}

// TestVerify_RoleAndPersonRequired: a non-approver and a credential that
// belongs to no user are both refused.
func TestVerify_RoleAndPersonRequired(t *testing.T) {
	env := newVerifyEnv(t)
	id := env.completedPlan(t)
	// A real grc_engineer: an approver by role (#2428), but verify is an
	// action-plan write, which only a tenant admin makes (#2507). The role
	// gate refuses it, before the owner rule is reached.
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.nonApprJWT, nil); code != http.StatusForbidden {
		t.Fatalf("grc_engineer verify: status %d, want 403: %v", code, body)
	}
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.machineJWT, nil); code != http.StatusForbidden {
		t.Fatalf("machine verify: status %d, want 403: %v", code, body)
	}
	if got := env.status(t, id); got != actionplan.StatusCompleted {
		t.Fatalf("status %q, want completed", got)
	}
}

// TestVerify_LinkedControlsRefused: a plan with a linked control is refused
// with 409; its controls' evaluations verify it.
func TestVerify_LinkedControlsRefused(t *testing.T) {
	env := newVerifyEnv(t)
	id := env.completedPlan(t)
	control := seedControl(t, env.admin, env.tenant)
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/controls/"+control.String(), env.ownerJWT, nil); code != http.StatusOK {
		t.Fatalf("link control: %d: %v", code, body)
	}
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.apprJWT, nil); code != http.StatusConflict {
		t.Fatalf("verify with a linked control: status %d, want 409: %v", code, body)
	}
}

// TestVerify_OnlyFromCompleted: verify on a plan that is not completed is
// an illegal transition.
func TestVerify_OnlyFromCompleted(t *testing.T) {
	env := newVerifyEnv(t)
	code, body := env.do(t, http.MethodPost, "/v1/action-plans", env.ownerJWT, map[string]any{
		"title": "Draft plan", "owner_id": env.owner.String(),
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d: %v", code, body)
	}
	id := body["action_plan"].(map[string]any)["id"].(string)
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+id+"/verify", env.apprJWT, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("verify a draft: status %d, want 422: %v", code, body)
	}
}

// TestVerify_PatchCannotSetVerified: PATCH status=verified is refused for
// everyone, so the owner rule cannot be bypassed through the edit route.
func TestVerify_PatchCannotSetVerified(t *testing.T) {
	env := newVerifyEnv(t)
	id := env.completedPlan(t)
	for _, bearer := range []string{env.ownerJWT, env.apprJWT} {
		if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, bearer, map[string]any{"status": "verified"}); code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH status=verified: %d, want 422: %v", code, body)
		}
	}
	if got := env.status(t, id); got != actionplan.StatusCompleted {
		t.Fatalf("status %q, want completed", got)
	}
}

// TestVerify_StoreRefusesMachine: the store refuses an approver with no
// person behind it.
func TestVerify_StoreRefusesMachine(t *testing.T) {
	env := newVerifyEnv(t)
	id := uuid.MustParse(env.completedPlan(t))
	_, err := env.store.Verify(ctxFor(t, env.tenant), id, approval.Approver{CredentialID: "key_machine"})
	if !errors.Is(err, actionplan.ErrVerifierNotPerson) {
		t.Fatalf("machine verify: got %v, want ErrVerifierNotPerson", err)
	}
}

// TestEdit_OwnerMustBeActiveTenantUser: the edit route refuses a disabled
// user and a user from another tenant as the new owner, and takes an
// active one.
func TestEdit_OwnerMustBeActiveTenantUser(t *testing.T) {
	env := newVerifyEnv(t)
	id := env.completedPlan(t)

	disabled := seedUser(t, env.admin, env.tenant)
	if _, err := env.admin.Exec(context.Background(), `UPDATE users SET status = 'disabled' WHERE id = $1`, disabled); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	otherTenant := seedTenant(t, env.admin)
	stranger := seedUser(t, env.admin, otherTenant)

	for name, owner := range map[string]uuid.UUID{"disabled": disabled, "other tenant": stranger} {
		if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, env.ownerJWT, map[string]any{"owner_id": owner.String()}); code != http.StatusBadRequest {
			t.Fatalf("%s owner: status %d, want 400: %v", name, code, body)
		}
	}
	code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, env.ownerJWT, map[string]any{"owner_id": env.approver.String()})
	if code != http.StatusOK {
		t.Fatalf("active owner: status %d, want 200: %v", code, body)
	}
	if got := body["action_plan"].(map[string]any)["owner_id"]; got != env.approver.String() {
		t.Fatalf("owner_id = %v, want %s", got, env.approver)
	}
}

// verify posts the verify action for plan as bearer and returns the status.
func (env verifyEnv) verify(t *testing.T, plan, bearer string) int {
	t.Helper()
	code, _ := env.do(t, http.MethodPost, "/v1/action-plans/"+plan+"/verify", bearer, nil)
	return code
}

func (env verifyEnv) reassign(t *testing.T, plan, bearer string, to uuid.UUID) {
	t.Helper()
	if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+plan, bearer, map[string]any{"owner_id": to.String()}); code != http.StatusOK {
		t.Fatalf("reassign to %s: %d: %v", to, code, body)
	}
}

// TestVerify_ReassignBypassRefused is #2434's bypass: an approver who owns
// a plan hands it to a colleague, verifies, and takes it back. Every step
// is audited, and the verify is still refused: the owner completed the
// plan and owned it after completion.
func TestVerify_ReassignBypassRefused(t *testing.T) {
	env := newVerifyEnv(t)
	id := env.completedPlan(t)

	env.reassign(t, id, env.ownerJWT, env.approver)
	if code := env.verify(t, id, env.ownerJWT); code != http.StatusForbidden {
		t.Fatalf("former owner verifies after handing the plan off: %d, want 403", code)
	}
	env.reassign(t, id, env.ownerJWT, env.owner)
	if got := env.status(t, id); got != actionplan.StatusCompleted {
		t.Fatalf("status %q after the refused bypass, want completed", got)
	}
	// The colleague owned it after completion too.
	if code := env.verify(t, id, env.apprJWT); code != http.StatusForbidden {
		t.Fatalf("interim owner verify: %d, want 403", code)
	}
	// Someone with no part in it can.
	third := env.seedAdmin(t)
	if code := env.verify(t, id, env.jwtFor(t, third)); code != http.StatusOK {
		t.Fatalf("uninvolved admin verify: %d, want 200", code)
	}
}

// TestVerify_CompleterRefused: whoever moved the plan to completed cannot
// verify it, though they never owned it.
func TestVerify_CompleterRefused(t *testing.T) {
	env := newVerifyEnv(t)
	code, body := env.do(t, http.MethodPost, "/v1/action-plans", env.ownerJWT, map[string]any{
		"title": "Completed by someone else", "owner_id": env.owner.String(),
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d: %v", code, body)
	}
	id := body["action_plan"].(map[string]any)["id"].(string)
	for _, s := range []string{actionplan.StatusInProgress, actionplan.StatusCompleted} {
		if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, env.apprJWT, map[string]any{"status": s}); code != http.StatusOK {
			t.Fatalf("PATCH status %s: %d: %v", s, code, body)
		}
	}
	if code := env.verify(t, id, env.apprJWT); code != http.StatusForbidden {
		t.Fatalf("completer verify: %d, want 403", code)
	}
	third := env.seedAdmin(t)
	if code := env.verify(t, id, env.jwtFor(t, third)); code != http.StatusOK {
		t.Fatalf("uninvolved admin verify: %d, want 200", code)
	}
}

// TestVerify_GuardReadsUnderLock forces the interleaving the row lock
// closes (#2434): a control link is inserted, uncommitted, before verify
// starts. Verify waits on the plan row; once the link commits it sees the
// control and refuses with ErrVerifyHasControls. Without FOR UPDATE the
// guard counts no control, the status UPDATE does not conflict with the
// link's KEY SHARE, and the plan is verified with a control linked.
func TestVerify_GuardReadsUnderLock(t *testing.T) {
	env := newVerifyEnv(t)
	id := uuid.MustParse(env.completedPlan(t))
	control := seedControl(t, env.admin, env.tenant)

	ctx := context.Background()
	tx, err := env.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO action_plan_controls (action_plan_id, control_id, tenant_id, linked_by)
		VALUES ($1, $2, $3, $4)`, id, control, env.tenant, env.owner); err != nil {
		t.Fatalf("link control in tx: %v", err)
	}

	third := env.seedAdmin(t)
	verifyCtx := ctxFor(t, env.tenant)
	done := make(chan error, 1)
	go func() {
		_, err := env.store.Verify(verifyCtx, id, approval.Approver{UserID: third.String()})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("verify returned (%v) while the link was uncommitted; want it to wait on the plan row", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit link: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, actionplan.ErrVerifyHasControls) {
			t.Fatalf("verify after the link committed: %v, want ErrVerifyHasControls", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("verify still waiting 10s after the link committed")
	}
}
