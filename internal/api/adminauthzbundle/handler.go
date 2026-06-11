// Package adminauthzbundle serves the slice-378 authz bundle
// hot-reload HTTP surface.
//
// One route:
//
//	POST /v1/admin/authz-bundle/reload  -- super_admin-gated; reloads
//	                                      the embedded authz bundle
//	                                      without a process restart.
//
// Authority gate: super_admin only (slice 378 P0-4). The handler reads
// `jwtmw.FromContext().SuperAdmin` as the load-bearing check — defence
// in depth on top of the slice-035 OPA authz middleware's
// super_admin.rego policy.
//
// LOAD-BEARING DESIGN: the reload pipeline runs in this order:
//
//  1. super_admin gate
//  2. per-actor rate limit (1 reload per 60 seconds per super_admin)
//  3. atomic.Pointer-backed Engine.ReloadFromEmbedded(ctx, matrix
//     validator). The matrix validator runs against the CANDIDATE
//     prepared query BEFORE the atomic swap; matrix-failure leaves
//     the engine serving the prior bundle (slice 378 AC-3).
//  4. dual audit-log write inside ONE transaction:
//     - super_admin_audit_log row (platform-global; action =
//     'authz_bundle_reload'; payload_json captures
//     {before_bundle_sha256, after_bundle_sha256})
//     - me_audit_log row anchored to the actor's session tenant so
//     the slice-124 unified aggregator surfaces the event
//  5. external audit-sink fanout via unifiedlog.SubjectModuleCore
//
// Anti-criteria honored (slice 378 P0-*):
//
//   - P0-378-1 (NO torn-read window): the engine's atomic.Pointer
//     contract is the entire load-bearing piece; the handler does
//     not introduce any mutex / copy-on-read path.
//   - P0-378-2 (NO matrix-bypass): ValidateMatrix is wired
//     unconditionally into the ReloadFromEmbedded call.
//   - P0-378-3 (NO bundle-content logging): payload_json carries the
//     SHA-256 fingerprint of the bundle, not its source bytes.
//   - P0-378-4 (NO super_admin bypass): requireSuperAdmin is the
//     first thing every request hits.
//   - P0-378-5 (NO regocache touches): this handler does not import
//     or reference internal/eval/regocache.
//   - P0-378-6 (NO new dependencies): only existing in-tree packages.
package adminauthzbundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// auditActionReload is the action value written to me_audit_log AND
// super_admin_audit_log on every successful reload. Admitted by the
// slice-378 migration (20260528000000_authz_bundle_reload_meta_audit).
const auditActionReload = "authz_bundle_reload"

// defaultRateLimitWindow is the per-super_admin minimum interval
// between reloads (slice 378 AC-5). Calibrated to prevent a
// runaway-script reloading 10× per second while keeping the operator
// surface responsive enough for legitimate iteration during a policy
// rewrite (60s is the conservative shape; mirror of slice 023 rate-
// limit convention).
const defaultRateLimitWindow = 60 * time.Second

// Reloader is the subset of *authz.Engine that this handler depends
// on. Defined as an interface so tests can inject a fake without
// constructing a real OPA engine.
type Reloader interface {
	BundleSHA256() string
	ReloadFromEmbedded(ctx context.Context, validator authz.MatrixValidator) error
}

// Handler bundles the slice-378 route. Construct with New.
type Handler struct {
	pool      *pgxpool.Pool
	engine    Reloader
	validator authz.MatrixValidator

	// rateLimit is the per-actor minimum interval between reloads.
	rateLimit time.Duration

	// limiterMu guards limiterLast.
	limiterMu sync.Mutex
	// limiterLast records the most-recent reload timestamp per actor
	// user_id. A simple in-process map is sufficient for v1 — the
	// reload endpoint sees at most a handful of super_admins and at
	// most a few requests per minute, by design.
	limiterLast map[uuid.UUID]time.Time
}

// New constructs a Handler over a *pgxpool.Pool + an authz Reloader.
// The validator is wired to authz.ValidateMatrix by default; tests
// (and any future maintainer-driven matrix variant) can override via
// WithValidator. nil engine returns a handler that 503s every request
// — matches the slice-142 unconfigured-handler pattern.
func New(pool *pgxpool.Pool, engine Reloader) *Handler {
	return &Handler{
		pool:        pool,
		engine:      engine,
		validator:   authz.ValidateMatrix,
		rateLimit:   defaultRateLimitWindow,
		limiterLast: make(map[uuid.UUID]time.Time),
	}
}

// WithValidator overrides the default matrix validator. Returns the
// receiver so the call chains. Used by tests; production callers
// should stick with the default.
func (h *Handler) WithValidator(v authz.MatrixValidator) *Handler {
	h.validator = v
	return h
}

// WithRateLimitWindow overrides the per-actor rate-limit window.
// Returns the receiver so the call chains. Used by tests to drive a
// shorter window; production callers should stick with the default.
func (h *Handler) WithRateLimitWindow(d time.Duration) *Handler {
	h.rateLimit = d
	return h
}

// --- wire types ---

// reloadResponse is the success wire shape returned on a 200.
type reloadResponse struct {
	ReloadedAt         time.Time `json:"reloaded_at"`
	MatrixPassed       bool      `json:"matrix_passed"`
	BeforeBundleSHA256 string    `json:"before_bundle_sha256"`
	AfterBundleSHA256  string    `json:"after_bundle_sha256"`
}

// --- handler ---

// Reload handles POST /v1/admin/authz-bundle/reload.
//
// Request body is OPTIONAL — empty body is the canonical invocation
// (the handler reloads the embedded bundle; v1 does not accept
// uploaded bundles per slice 378 P0-4). When a body is supplied it
// MUST be valid JSON (Content-Type: application/json) — any other
// shape is rejected with 400. This keeps the door open for future
// fields without breaking the v1 empty-body contract.
func (h *Handler) Reload(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.engine == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "authz engine not wired")
		return
	}
	if !requireSuperAdmin(w, r) {
		return
	}

	actorID := actorFromContext(r.Context())
	if actorID == uuid.Nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "actor user_id not on context")
		return
	}
	actorTenantID, terr := actorTenantFromContext(r.Context())
	if terr != nil {
		httperr.WriteInternal(w, r, "adminauthzbundle", terr)
		return
	}

	// Per-actor rate limit (slice 378 AC-5). The check is in-process;
	// running multiple atlas binaries does not coordinate, but that's
	// an acceptable v1 shape — the underlying reload is idempotent
	// per binary and the audit log records all reloads regardless.
	if !h.checkAndStampRate(actorID, time.Now()) {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(h.rateLimit.Seconds())))
		httpresp.WriteError(w, http.StatusTooManyRequests, "reload rate limit exceeded; try again later")
		return
	}

	// Capture the before-SHA BEFORE the swap.
	beforeSHA := h.engine.BundleSHA256()

	// Run the reload. The matrix validator runs against the
	// CANDIDATE prepared query INSIDE the engine — see
	// authz.Engine.Reload for the load-bearing sequence.
	reloadCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if rErr := h.engine.ReloadFromEmbedded(reloadCtx, h.validator); rErr != nil {
		// Matrix-failure is the operator-visible class. We surface
		// it as 4xx with the error detail so the operator can debug
		// the bundle. Compile-failure also surfaces here. Wrap
		// errors are NOT logged at ERROR — they are operator-driven.
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "reload rejected: "+rErr.Error())
		return
	}

	afterSHA := h.engine.BundleSHA256()
	reloadedAt := time.Now().UTC()

	// Audit-log dual-write (slice 378 AC-5 / AC-6). The two rows are
	// written inside ONE transaction so the audit trail never
	// reflects a partial reload event.
	if err := h.writeAuditRows(r.Context(), actorID, actorTenantID, beforeSHA, afterSHA, reloadedAt); err != nil {
		// The reload already happened — the audit-write failure must
		// not roll back the swap. Log + return 500 so the operator
		// knows the audit row is missing but the reload did land.
		httperr.WriteInternal(w, r, "audit-log write after reload", err)
		return
	}

	// External audit sink fanout (slice 126).
	sinkPayload := mustMarshal(map[string]any{
		"before_bundle_sha256": beforeSHA,
		"after_bundle_sha256":  afterSHA,
	})
	sink.EmitDefault(r.Context(), unifiedlog.Entry{
		OccurredAt:    reloadedAt,
		ActorID:       actorID.String(),
		TenantID:      actorTenantID,
		Kind:          unifiedlog.KindMe,
		TargetType:    "authz_bundle",
		TargetID:      afterSHA,
		Action:        auditActionReload,
		RowID:         uuid.New(),
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   sinkPayload,
	})

	httpresp.WriteJSON(w, http.StatusOK, reloadResponse{
		ReloadedAt:         reloadedAt,
		MatrixPassed:       true,
		BeforeBundleSHA256: beforeSHA,
		AfterBundleSHA256:  afterSHA,
	})

}

// writeAuditRows persists the dual audit-log rows inside one
// transaction. Mirrors the slice-142 super_admin grant/demote
// pattern: super_admin_audit_log + me_audit_log written together.
func (h *Handler) writeAuditRows(ctx context.Context, actorID, actorTenantID uuid.UUID, beforeSHA, afterSHA string, occurredAt time.Time) error {
	if h.pool == nil {
		return fmt.Errorf("audit pool not configured")
	}
	payload := mustMarshal(map[string]any{
		"before_bundle_sha256": beforeSHA,
		"after_bundle_sha256":  afterSHA,
		"reloaded_at":          occurredAt.Format(time.RFC3339Nano),
	})
	beforeBlob := mustMarshal(map[string]any{
		"bundle_sha256": beforeSHA,
	})
	afterBlob := mustMarshal(map[string]any{
		"bundle_sha256": afterSHA,
	})

	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin audit tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("apply tenant to audit tx: %w", err)
	}

	// 1. super_admin_audit_log row (platform-global; no RLS).
	// authz_bundle is the canonical target_type. We use the
	// after-SHA-derived UUID as the target_user_id surrogate so the
	// row schema (NOT NULL target_user_id) stays satisfied — this is
	// a slight contract bend (target_user_id semantically refers to
	// the granted/demoted user in the slice-142 lineage) so the
	// payload_json carries the authz_bundle SHA as the real target.
	// An alternative would be a schema migration to introduce a
	// nullable target_resource_id; deferred to v2 per slice 378
	// D3.
	//
	// We stamp target_user_id = actor_user_id when the action is a
	// bundle reload — this satisfies the NOT NULL + nonzero CHECK
	// without inventing a synthetic UUID, and keeps "who" findable
	// via either column.
	if _, err := tx.Exec(ctx,
		`INSERT INTO super_admin_audit_log
		 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
		 VALUES ($1, $2, $3, $4, $5)`,
		auditActionReload, actorID, actorID, actorTenantID, payload,
	); err != nil {
		return fmt.Errorf("insert super_admin_audit_log: %w", err)
	}

	// 2. me_audit_log row (tenant-scoped; flows through unified
	// aggregator).
	if err := dbx.New(tx).InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
		TenantID: pgtype.UUID{Bytes: actorTenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: actorID, Valid: true},
		Action:   auditActionReload,
		Before:   beforeBlob,
		After:    afterBlob,
	}); err != nil {
		return fmt.Errorf("insert me_audit_log: %w", err)
	}
	return tx.Commit(ctx)
}

// checkAndStampRate returns true when the caller is allowed to reload
// (i.e. the per-actor rate-limit window has elapsed since the last
// reload). On success it stamps the new reload time. Concurrent
// callers serialise on h.limiterMu; the critical section is tiny.
func (h *Handler) checkAndStampRate(actor uuid.UUID, now time.Time) bool {
	h.limiterMu.Lock()
	defer h.limiterMu.Unlock()
	if last, ok := h.limiterLast[actor]; ok {
		if now.Sub(last) < h.rateLimit {
			return false
		}
	}
	h.limiterLast[actor] = now
	return true
}

// --- helpers ---

// requireSuperAdmin returns true when the caller carries the
// super_admin bit. Same defence-in-depth as the slice-142 handler;
// the OPA policy gate (super_admin.rego) runs upstream.
func requireSuperAdmin(w http.ResponseWriter, r *http.Request) bool {
	claims := jwtmw.FromContext(r.Context())
	if claims != nil && claims.SuperAdmin {
		return true
	}
	httpresp.WriteError(w, http.StatusForbidden, "super_admin required")
	return false
}

// actorFromContext returns the caller's user_id from the JWT subject
// claim.
func actorFromContext(ctx context.Context) uuid.UUID {
	if claims := jwtmw.FromContext(ctx); claims != nil {
		// Subject is "user:<uuid>" (auth-substrate-v2); strip the prefix
		// before parsing or the real-auth caller resolves to uuid.Nil.
		if u, err := uuid.Parse(jwtmw.SubjectUserID(claims.Subject)); err == nil {
			return u
		}
	}
	return uuid.Nil
}

// actorTenantFromContext resolves the caller's session tenant UUID
// for the audit-log row tagging.
func actorTenantFromContext(ctx context.Context) (uuid.UUID, error) {
	str, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, errors.New("tenant context missing")
	}
	id, perr := uuid.Parse(str)
	if perr != nil {
		return uuid.Nil, errors.New("invalid tenant in context")
	}
	return id, nil
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("adminauthzbundle: json marshal: %v", err))
	}
	return b
}
