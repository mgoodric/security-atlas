//go:build integration

// #2109 — action plans verified from evidence. After each control
// evaluation, a completed plan whose linked controls all pass their latest
// evaluation becomes verified, and a verified plan with a linked control
// that now fails returns to completed. Both moves are the system's: made
// by action_plans_reconcile_verification inside the evaluation's
// transaction and logged under actionplan.SystemActor. These tests drive
// the real evaluation engine against real Postgres under RLS.
//
// Run with:
//
//	go test -tags=integration ./internal/actionplan/ -run Reconcile

package actionplan_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// reconcileEnv is the verify env plus the evaluation engine and the
// tables evaluation writes, cleaned before the controls they reference.
type reconcileEnv struct {
	verifyEnv
	app    *pgxpool.Pool
	engine *eval.Engine
}

func newReconcileEnv(t *testing.T) reconcileEnv {
	t.Helper()
	env := newVerifyEnv(t)
	app := dbtest.NewAppPool(t)
	t.Cleanup(func() {
		// Registered after SeedTenant's cleanup, so it runs first: the
		// evaluation rows and evidence FK the controls.
		for _, table := range []string{"control_evaluations", "evidence_records"} {
			if _, err := env.admin.Exec(context.Background(),
				"DELETE FROM "+table+" WHERE tenant_id = $1", env.tenant); err != nil {
				t.Logf("cleanup %s: %v", table, err)
			}
		}
	})
	return reconcileEnv{verifyEnv: env, app: app, engine: eval.NewEngine(eval.NewStore(app), scope.NewStore(app))}
}

// evaluatedControl seeds a manual_attested control the engine evaluates
// from its freshest evidence record.
func (env reconcileEnv) evaluatedControl(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := env.admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, '#2109 control', 'IAC', 'manual_attested',
		        $3, 'monthly', '[]'::jsonb, 'true')
	`, id, env.tenant, "test-bundle-2109-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// evaluate records evidence with result for the control and evaluates it,
// as a push followed by the evaluation consumer would.
func (env reconcileEnv) evaluate(t *testing.T, control uuid.UUID, result string) {
	t.Helper()
	ev := uuid.New()
	if _, err := env.admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, $5, '{}'::jsonb, $6, $7)
	`, ev, env.tenant, control, time.Now().UTC(), result, "hash-2109-"+ev.String()[:8], control.String()); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	if _, err := env.engine.EvaluateControl(ctxFor(t, env.tenant), control, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}
}

func (env reconcileEnv) link(t *testing.T, plan string, control uuid.UUID) {
	t.Helper()
	if code, body := env.do(t, http.MethodPost, "/v1/action-plans/"+plan+"/controls/"+control.String(), env.ownerJWT, nil); code != http.StatusOK {
		t.Fatalf("link control: %d: %v", code, body)
	}
}

// systemMoves returns the plan's status_changed rows the system wrote, as
// before -> after statuses, oldest first. Every one must name no person.
func (env reconcileEnv) systemMoves(t *testing.T, plan string) [][2]string {
	t.Helper()
	entries, err := env.store.ListAuditLog(ctxFor(t, env.tenant), uuid.MustParse(plan))
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	var out [][2]string
	for _, e := range entries {
		if e.SystemActor == "" {
			continue
		}
		if e.SystemActor != actionplan.SystemActor || e.ActorID != uuid.Nil || e.ActionType != actionplan.ActionStatusChanged {
			t.Fatalf("system row: actor %q / %s, action %s", e.SystemActor, e.ActorID, e.ActionType)
		}
		out = append(out, [2]string{statusOf(t, e.BeforeState), statusOf(t, e.AfterState)})
	}
	return out
}

func statusOf(t *testing.T, raw []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("audit state: %v", err)
	}
	s, _ := m["status"].(string)
	return s
}

func (env reconcileEnv) controlsFailing(t *testing.T, plan string) bool {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/v1/action-plans/"+plan, env.apprJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("GET plan: %d: %v", code, body)
	}
	return body["action_plan"].(map[string]any)["controls_failing"].(bool)
}

// TestReconcile_PassVerifiesFailReturnsToCompleted is the ticket's first
// check: completing a plan whose linked control passes makes it verified;
// the control failing later returns it to completed; both are logged under
// the system actor.
func TestReconcile_PassVerifiesFailReturnsToCompleted(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)

	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("after a passing evaluation: status %q, want verified", got)
	}
	if env.controlsFailing(t, plan) {
		t.Fatal("verified plan reports controls_failing")
	}

	env.evaluate(t, control, "fail")
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("after a failing evaluation: status %q, want completed", got)
	}
	if !env.controlsFailing(t, plan) {
		t.Fatal("completed plan with a failing control: controls_failing false")
	}

	want := [][2]string{
		{actionplan.StatusCompleted, actionplan.StatusVerified},
		{actionplan.StatusVerified, actionplan.StatusCompleted},
	}
	got := env.systemMoves(t, plan)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("system audit moves %v, want %v", got, want)
	}
}

// TestReconcile_FailingControlStaysCompleted is the ticket's second check:
// a completed plan with a failing control stays completed, and the API
// exposes the still-failing flag on the plan and on the control's and
// risk's linked-plan lists.
func TestReconcile_FailingControlStaysCompleted(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)

	env.evaluate(t, control, "fail")
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("status %q, want completed", got)
	}
	if !env.controlsFailing(t, plan) {
		t.Fatal("GET plan: controls_failing false, want true")
	}
	code, body := env.do(t, http.MethodGet, "/v1/controls/"+control.String()+"/action-plans", env.apprJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("GET control plans: %d: %v", code, body)
	}
	refs := body["action_plans"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["controls_failing"] != true {
		t.Fatalf("control's linked plans: %v, want one with controls_failing true", refs)
	}
	if moves := env.systemMoves(t, plan); len(moves) != 0 {
		t.Fatalf("system moved a plan whose control fails: %v", moves)
	}
}

// TestReconcile_EveryLinkedControlMustPass: one passing control of two
// does not verify the plan; the plan verifies on the evaluation that makes
// the second pass. A control never evaluated counts as not passing.
func TestReconcile_EveryLinkedControlMustPass(t *testing.T) {
	env := newReconcileEnv(t)
	a, b := env.evaluatedControl(t), env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, a)
	env.link(t, plan, b)

	env.evaluate(t, a, "pass")
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("one of two controls evaluated: status %q, want completed", got)
	}
	env.evaluate(t, b, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("both controls pass: status %q, want verified", got)
	}
}

// TestReconcile_OnlyCompletedPlansVerify: an in-progress plan whose control
// passes is not moved; the system verifies only what a person completed.
func TestReconcile_OnlyCompletedPlansVerify(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	code, body := env.do(t, http.MethodPost, "/v1/action-plans", env.ownerJWT, map[string]any{
		"title": "In progress", "owner_id": env.owner.String(),
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d: %v", code, body)
	}
	plan := body["action_plan"].(map[string]any)["id"].(string)
	if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+plan, env.ownerJWT, map[string]any{"status": "in_progress"}); code != http.StatusOK {
		t.Fatalf("start: %d: %v", code, body)
	}
	env.link(t, plan, control)
	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusInProgress {
		t.Fatalf("status %q, want in_progress", got)
	}
}

// TestReconcile_NoHTTPRouteMakesTheSystemMoves: no person or key moves a
// plan with linked controls to verified, or a verified plan back to
// completed, through any action-plan route.
func TestReconcile_NoHTTPRouteMakesTheSystemMoves(t *testing.T) {
	env := newReconcileEnv(t)
	bearers := map[string]string{"owner": env.ownerJWT, "approver": env.apprJWT, "key": env.machineJWT, "grc_engineer": env.nonApprJWT}

	// A completed plan whose control fails: verify and PATCH are refused.
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)
	env.evaluate(t, control, "fail")
	for who, bearer := range bearers {
		if code, _ := env.do(t, http.MethodPost, "/v1/action-plans/"+plan+"/verify", bearer, nil); code < 400 {
			t.Errorf("%s: POST verify on a plan with linked controls: %d, want refused", who, code)
		}
		if code, _ := env.do(t, http.MethodPatch, "/v1/action-plans/"+plan, bearer, map[string]any{"status": "verified"}); code < 400 {
			t.Errorf("%s: PATCH status=verified: %d, want refused", who, code)
		}
	}
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("status %q after the refused attempts, want completed", got)
	}

	// A verified plan: PATCH back to completed, or anywhere, is refused.
	// A fresh control and plan, since the failing record above stays in
	// the first control's evaluation window.
	control = env.evaluatedControl(t)
	plan = env.completedPlan(t)
	env.link(t, plan, control)
	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q, want verified", got)
	}
	for who, bearer := range bearers {
		for _, to := range []string{actionplan.StatusCompleted, actionplan.StatusInProgress} {
			if code, _ := env.do(t, http.MethodPatch, "/v1/action-plans/"+plan, bearer, map[string]any{"status": to}); code < 400 {
				t.Errorf("%s: PATCH verified -> %s: %d, want refused", who, to, code)
			}
		}
	}
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q after the refused attempts, want verified", got)
	}
}

// TestReconcile_TriggerRefusesUnverifyOutsideTheFunction: the app role
// cannot make verified -> completed or forge a system audit row, even
// with the marker GUC set and even as atlas_service_account (the role
// atlas_app can SET ROLE to). Only the reconcile function's owner can.
func TestReconcile_TriggerRefusesUnverifyOutsideTheFunction(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)
	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q, want verified", got)
	}

	for _, role := range []string{"", "atlas_service_account"} {
		err := env.appTx(t, role, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE action_plans SET status = 'completed' WHERE id = $1`, plan)
			return err
		})
		// atlas_service_account holds no grant on action_plans, so it is
		// refused before the trigger runs; either refusal holds the line.
		if !isPgCode(err, "23514") && !(role != "" && isPgCode(err, "42501")) {
			t.Errorf("role %q: verified -> completed with the marker set: %v, want check_violation", role, err)
		}
		err = env.appTx(t, role, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO action_plan_audit_log (tenant_id, action_plan_id, system_actor, action_type)
				VALUES ($1, $2, $3, 'status_changed')`, env.tenant, plan, actionplan.SystemActor)
			return err
		})
		if !isPgCode(err, "42501") {
			t.Errorf("role %q: forged system audit row: %v, want insufficient_privilege", role, err)
		}
	}
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q, want verified", got)
	}

	// The function itself refuses without a tenant context.
	if _, err := env.app.Exec(context.Background(), `SELECT action_plans_reconcile_verification($1)`, control); !isPgCode(err, "42501") {
		t.Fatalf("reconcile without a tenant: %v, want insufficient_privilege", err)
	}
}

// appTx runs fn as the app role in the tenant, with the reconcile
// function's marker GUC set, after SET LOCAL ROLE role when role is set.
// The transaction always rolls back.
func (env reconcileEnv) appTx(t *testing.T, role string, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := env.app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, env.tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if role != "" {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
			t.Fatalf("SET LOCAL ROLE %s: %v", role, err)
		}
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('atlas.action_plan_system_move', 'on', true)`); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	return fn(ctx, tx)
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
