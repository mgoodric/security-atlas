// evaluate.go — POST /v1/controls/{id}/evaluate, "Evaluate now" (#2110).
//
// The route runs evaluation for one control synchronously and returns the
// control's new state in the GET /v1/controls/{id}/state shape. It calls the
// same eval.Engine.EvaluateControl the ingest subscriber and scheduler call,
// so the tenant transaction, the action-plan reconcile (#2109) and the
// control_evaluations append behave identically; only the trigger differs
// (manual).
//
// Who may evaluate (Matt's call, 2026-11-16, decisions log
// docs/audit-log/2110-evaluate-now-decisions.md): a tenant admin
// (authz.IsTenantAdmin), or the person assigned as the control's owner
// (control_owner_assignments.owner_user_id), compared by
// credstore.MinterUserID so a JWT "user:" session and a user-bound API key
// both match and an unbound key never does. OPA admits control_owner at
// this one path (control_owner.rego); the per-control check is here.
//
// Abuse: concurrent calls for one control share one evaluation
// (single-flight keyed by tenant + control), and an evaluation is bounded by
// evaluateTimeout. The shared run is detached from any one caller's
// cancellation so a caller that hangs up does not fail the others. Each
// principal may start DefaultEvaluateRatePerMin evaluations a minute
// (evaluate_limit.go, #2479); past that the call is 429 with Retry-After.
//
// Who ran it (#2479): the run records the caller's credstore.PrincipalOf as
// control_evaluations.triggered_by. A caller who joins a run already in
// flight shares it, so the rows name the caller who started it.
package controlstate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/ratelimit"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// evaluateTimeout bounds one synchronous evaluation.
const evaluateTimeout = 30 * time.Second

// controlRunner is the evaluate seam: run evaluation, then read the state
// back. *eval.Engine satisfies it.
type controlRunner interface {
	EvaluateControlBy(ctx context.Context, controlID uuid.UUID, trigger, actor string, asOf time.Time) (int, error)
	ControlState(ctx context.Context, controlID uuid.UUID, scopePredicate string, asOf time.Time) ([]eval.State, error)
}

// ownerLookup returns the users(id) assigned to own a control, or "" when
// none is assigned (or the control is not in the caller's tenant).
type ownerLookup func(ctx context.Context, controlID uuid.UUID) (string, error)

// EvaluateHandler serves POST /v1/controls/{id}/evaluate.
type EvaluateHandler struct {
	runner  controlRunner
	ownerOf ownerLookup
	timeout time.Duration
	flight  singleflight.Group
	limiter *ratelimit.Limiter
}

// NewEvaluateHandler wires the handler over the eval engine and the app-role
// pool the owner assignment is read through (RLS-scoped to the caller's
// tenant). ratePerMin is the per-principal limit; 0 or less uses
// DefaultEvaluateRatePerMin.
func NewEvaluateHandler(engine *eval.Engine, pool *pgxpool.Pool, ratePerMin int) *EvaluateHandler {
	return &EvaluateHandler{
		runner:  engine,
		ownerOf: poolOwnerLookup(pool),
		timeout: evaluateTimeout,
		limiter: newEvaluateLimiter(ratePerMin, nil),
	}
}

// Evaluate handles POST /v1/controls/{id}/evaluate.
func (h *EvaluateHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenancy.TenantFromContext(r.Context())
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential missing")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}

	if !authz.IsTenantAdmin(cred) {
		owner, err := h.ownerOf(r.Context(), controlID)
		if err != nil {
			httperr.WriteInternal(w, r, "controlstate evaluate owner", err)
			return
		}
		caller := credstore.MinterUserID(cred)
		if caller == "" || owner == "" || caller != owner {
			httpresp.WriteError(w, http.StatusForbidden, "only the control's owner or an admin may evaluate it")
			return
		}
	}

	// Checked after authz, so a caller who may not evaluate never spends
	// a token, and before single-flight, so a joiner counts too.
	actor := credstore.PrincipalOf(cred)
	if h.limiter != nil {
		if ok, wait := h.limiter.Allow(actor); !ok {
			ratelimit.SetRetryAfter(w, ratelimit.RetryAfterSeconds(wait))
			httpresp.WriteError(w, http.StatusTooManyRequests, "too many evaluations; retry later")
			return
		}
	}

	// One evaluation per (tenant, control) at a time; callers that arrive
	// while it runs share its result.
	key := tenantID + "/" + controlID.String()
	runCtx := context.WithoutCancel(r.Context())
	res, err, _ := h.flight.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(runCtx, h.timeout)
		defer cancel()
		if _, err := h.runner.EvaluateControlBy(ctx, controlID, eval.TriggerManual, actor, eval.FarFuture); err != nil {
			return nil, err
		}
		return h.runner.ControlState(ctx, controlID, "", eval.FarFuture)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			httpresp.WriteError(w, http.StatusGatewayTimeout, "evaluation timed out")
			return
		}
		writeStateErr(w, r, err)
		return
	}
	states, _ := res.([]eval.State)
	out := make([]stateWire, len(states))
	for i, s := range states {
		out[i] = stateWireFrom(s)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id": controlID.String(),
		"states":     out,
		"count":      len(out),
	})
}

// poolOwnerLookup reads control_owner_assignments in a tenant-GUC
// transaction, so RLS confines it to the caller's tenant.
func poolOwnerLookup(pool *pgxpool.Pool) ownerLookup {
	return func(ctx context.Context, controlID uuid.UUID) (string, error) {
		tenantStr, err := tenancy.TenantFromContext(ctx)
		if err != nil {
			return "", err
		}
		tenantID, err := uuid.Parse(tenantStr)
		if err != nil {
			return "", fmt.Errorf("parse tenant id: %w", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return "", fmt.Errorf("begin: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return "", err
		}
		row, err := dbx.New(tx).GetControlOwnerAssignment(ctx, dbx.GetControlOwnerAssignmentParams{
			TenantID:  pgtype.UUID{Bytes: tenantID, Valid: true},
			ControlID: pgtype.UUID{Bytes: controlID, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if !row.OwnerUserID.Valid {
			return "", nil
		}
		return uuid.UUID(row.OwnerUserID.Bytes).String(), nil
	}
}
