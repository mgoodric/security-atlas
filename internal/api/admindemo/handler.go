package admindemo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/demoseed"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// demoEnableEnvVar matches slice 205's CLI gate so the operator
	// flips ONE env var to enable both surfaces. Slice 278 D1.
	demoEnableEnvVar = "ATLAS_ENABLE_DEMO_SEED"

	// demoTenantSlug is the hard-coded slug for the HTTP button path.
	// P0-278-2: no user input flows through. The CLI accepts custom
	// slugs; the UI button does not.
	demoTenantSlug = "demo"

	// auditActionSeed and auditActionTeardown are the action values
	// the HTTP handler writes to me_audit_log + super_admin_audit_log
	// per invocation. Distinct from slice 205's demo_seed_apply /
	// demo_seed_teardown which the seeder writes to record the
	// seeder run itself.
	auditActionSeed     = "demo_seed"
	auditActionTeardown = "demo_teardown"

	// rateLimitWindow is the per-IP minimum gap between seed/teardown
	// invocations. Slice 278 D3 (60s matches operator pace).
	rateLimitWindow = 60 * time.Second
)

// isEnabledFunc returns true when the env-var gate is satisfied.
// Injected at construction so tests can override without touching
// the real process env.
type isEnabledFunc func() bool

// DefaultEnabledFunc reads the documented env-var gate. Returns true
// only for the exact lowercase string "true" — conservative on
// purpose so "1" / "yes" / "TRUE" do not accidentally enable the
// feature.
func DefaultEnabledFunc() bool {
	return os.Getenv(demoEnableEnvVar) == "true"
}

// Handler bundles the three routes.
//
// Holds:
//
//   - authPool (BYPASSRLS atlas_migrate pool) — passed to
//     internal/demoseed which requires the BYPASSRLS pool (slice 205
//     LOAD-BEARING design).
//   - isEnabled — test-injectable env-var gate.
//   - limiter  — per-IP token bucket (1 token, 60s replenish).
//   - clock    — test-injectable clock.
type Handler struct {
	authPool  *pgxpool.Pool
	isEnabled isEnabledFunc
	limiter   *ipBucketLimiter
	clock     func() time.Time
}

// New constructs a Handler. authPool MUST be non-nil for the
// Seed/Teardown routes; the Status route works regardless. enabled
// is the env-var gate accessor; pass DefaultEnabledFunc unless
// overriding for tests.
func New(authPool *pgxpool.Pool, enabled isEnabledFunc) *Handler {
	if enabled == nil {
		enabled = DefaultEnabledFunc
	}
	return &Handler{
		authPool:  authPool,
		isEnabled: enabled,
		limiter:   newIPBucketLimiter(rateLimitWindow),
		clock:     time.Now,
	}
}

// WithClock overrides the clock. Test-only.
func (h *Handler) WithClock(fn func() time.Time) *Handler {
	h.clock = fn
	h.limiter.clock = fn
	return h
}

// statusResponse is the JSON body for GET /v1/admin/demo/status.
type statusResponse struct {
	Enabled bool `json:"enabled"`
}

// seedResponse is the JSON body for POST /v1/admin/demo/seed.
//
// Fields summarize the demo dataset just written; admin password
// and seed dataset contents are intentionally EXCLUDED. Admin
// password rotation goes through the standard /v1/admin/users
// surface; the seeder prints the password only via the CLI which
// is the one place we accept that operational risk.
//
// P0-278-8: this struct deliberately omits any field that would
// leak row-level dataset content.
type seedResponse struct {
	TenantID     string `json:"tenant_id"`
	TenantSlug   string `json:"tenant_slug"`
	Controls     int    `json:"controls"`
	Risks        int    `json:"risks"`
	Evidence     int    `json:"evidence"`
	AuditPeriods int    `json:"audit_periods"`
	Samples      int    `json:"samples"`
	Idempotent   bool   `json:"idempotent"`
}

// teardownResponse is the JSON body for POST /v1/admin/demo/teardown.
type teardownResponse struct {
	TenantSlug string `json:"tenant_slug"`
	Status     string `json:"status"`
}

// Status handles GET /v1/admin/demo/status.
//
// Returns {enabled: true|false} reflecting the env-var gate. The
// route is admin-gated upstream (OPA admin.rego), so a non-admin
// caller never reaches this handler.
//
// Status is NOT rate-limited — operators hit it on every admin
// page load and the cost is constant.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	// Defense-in-depth admin check. The OPA middleware is the
	// load-bearing gate; this is the secondary guard so a
	// misconfigured authz mount fails closed.
	if !h.requireAdmin(w, r) {
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, statusResponse{Enabled: h.isEnabled()})
}

// Seed handles POST /v1/admin/demo/seed.
//
// Wire shape:
//
//  1. Admin check (defense-in-depth; OPA already ran).
//  2. Env-var gate.
//  3. Rate-limit gate (per-IP, 60s).
//  4. Write me_audit_log + super_admin_audit_log rows BEFORE invoking.
//  5. Invoke demoseed.Seeder.Apply with default slug + scale.
//  6. Return summary JSON.
//
// FAIL CLOSED: if the audit-log write fails, the seeder does NOT
// run. The handler returns 500 and the operator can retry.
//
// HTTP-invocation action vs seeder action: this handler writes
// action='demo_seed' (the click). The seeder writes its own
// 'demo_seed_apply' row separately (the run). Two-row forensic
// separation distinguishes who-clicked from what-was-seeded.
func (h *Handler) Seed(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if !h.isEnabled() {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "demo seed not enabled on this deployment")
		return
	}
	if !h.limiter.allow(clientIP(r), h.clock()) {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rateLimitWindow.Seconds())))
		httpresp.WriteError(w, http.StatusTooManyRequests, "rate limit exceeded: one demo invocation per 60 seconds")
		return
	}
	if h.authPool == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "demo seed not available: no auth pool")
		return
	}

	actorID, actorTenantID, err := h.resolveActor(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "resolve actor", err)
		return
	}

	// Write the invocation audit row BEFORE running the seeder. If the
	// audit write fails, the seeder does NOT run (fail closed). The
	// auditPayload carries the actor IP for forensic attribution and
	// a small status field defaulting to 'pending' — the handler does
	// NOT come back to update this row on completion (the seeder's
	// own demo_seed_apply row records the outcome).
	auditPayload := map[string]any{
		"slug":   demoTenantSlug,
		"scale":  demoseed.DefaultScale,
		"ip":     clientIP(r),
		"status": "invoked",
	}
	if err := h.writeAuditRows(r.Context(), auditActionSeed, actorID, actorTenantID, auditPayload); err != nil {
		httperr.WriteInternal(w, r, "audit write failed; seed not invoked", err)
		return
	}

	// Invoke the seeder. Slice 205's Apply is idempotent on slug, so
	// a re-click on an already-seeded tenant returns Idempotent=true.
	seeder, err := demoseed.NewSeeder(h.authPool, demoseed.DefaultScale)
	if err != nil {
		httperr.WriteInternal(w, r, "build seeder", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := seeder.Apply(ctx, demoseed.ApplyInput{
		Slug:          demoTenantSlug,
		ActorUserID:   actorID,
		ActorTenantID: actorTenantID,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "seed failed", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, seedResponse{
		TenantID:     res.TenantID.String(),
		TenantSlug:   res.TenantSlug,
		Controls:     res.Controls,
		Risks:        res.Risks,
		Evidence:     res.Evidence,
		AuditPeriods: res.AuditPeriods,
		Samples:      res.Samples,
		Idempotent:   res.Idempotent,
	})

}

// Teardown handles POST /v1/admin/demo/teardown.
//
// Mirrors Seed exactly except: writes action='demo_teardown' instead
// of 'demo_seed' and invokes Seeder.Teardown instead of Apply.
func (h *Handler) Teardown(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if !h.isEnabled() {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "demo seed not enabled on this deployment")
		return
	}
	if !h.limiter.allow(clientIP(r), h.clock()) {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rateLimitWindow.Seconds())))
		httpresp.WriteError(w, http.StatusTooManyRequests, "rate limit exceeded: one demo invocation per 60 seconds")
		return
	}
	if h.authPool == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "demo seed not available: no auth pool")
		return
	}

	actorID, actorTenantID, err := h.resolveActor(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "resolve actor", err)
		return
	}

	auditPayload := map[string]any{
		"slug":   demoTenantSlug,
		"ip":     clientIP(r),
		"status": "invoked",
	}
	if err := h.writeAuditRows(r.Context(), auditActionTeardown, actorID, actorTenantID, auditPayload); err != nil {
		httperr.WriteInternal(w, r, "audit write failed; teardown not invoked", err)
		return
	}

	seeder, err := demoseed.NewSeeder(h.authPool, demoseed.DefaultScale)
	if err != nil {
		httperr.WriteInternal(w, r, "build seeder", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := seeder.Teardown(ctx, demoTenantSlug, actorID, actorTenantID); err != nil {
		httperr.WriteInternal(w, r, "teardown failed", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, teardownResponse{
		TenantSlug: demoTenantSlug,
		Status:     "deleted",
	})

}

// requireAdmin enforces the admin role. Defense in depth: the OPA
// middleware is the load-bearing gate via admin.rego's
// `has_role("admin")` check. This handler-level check guards
// against a misconfigured authz mount.
//
// Two paths admit:
//
//  1. cred.IsAdmin == true (slice 187+: claims.SuperAdmin set OR
//     legacy bearer-token API key with IsAdmin flag).
//  2. cred.OwnerRoles carries "admin" (slice 192+: per-tenant role
//     grant — the JWT carries Roles[currentTenant] = ["admin"]
//     which jwtmw maps into cred.OwnerRoles).
//
// On failure: 403 with a JSON error. On missing credential: 401.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if cred.IsAdmin {
		return true
	}
	for _, role := range cred.OwnerRoles {
		if role == "admin" {
			return true
		}
	}
	httpresp.WriteError(w, http.StatusForbidden, "admin role required")
	return false
}

// resolveActor reads actor user_id + actor tenant_id from the
// request context. Both are required for audit-log row tagging.
// subjectUserID strips the "user:" prefix the auth substrate places on
// JWT subjects (Subject = "user:<uuid>"). A bare UUID with no prefix is
// returned unchanged, so this is safe for both the JWT path and any
// legacy bare-UUID credential.
func subjectUserID(s string) string {
	return strings.TrimPrefix(s, "user:")
}

func (h *Handler) resolveActor(ctx context.Context) (uuid.UUID, uuid.UUID, error) {
	var actorID uuid.UUID
	if claims := jwtmw.FromContext(ctx); claims != nil {
		// The atlas JWT Subject is "user:<uuid>" (auth-substrate-v2
		// convention — see internal/api/auth/http.go and
		// internal/api/oauth/pkce.go). Strip the prefix before parsing;
		// a bare UUID (no prefix) survives TrimPrefix unchanged.
		if u, err := uuid.Parse(subjectUserID(claims.Subject)); err == nil {
			actorID = u
		}
	}
	if actorID == uuid.Nil {
		// Legacy bearer path — try the credstore credential's UserID.
		// jwtmw also populates this with the "user:<uuid>" Subject, so
		// strip the prefix here too.
		cred, _ := authctx.CredentialFromContext(ctx)
		if cred.UserID != "" {
			if u, err := uuid.Parse(subjectUserID(cred.UserID)); err == nil {
				actorID = u
			}
		}
	}
	if actorID == uuid.Nil {
		return uuid.Nil, uuid.Nil, errors.New("actor user_id missing")
	}
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("actor tenant_id missing")
	}
	tenantID, perr := uuid.Parse(tenantStr)
	if perr != nil {
		return uuid.Nil, uuid.Nil, errors.New("actor tenant_id invalid")
	}
	return actorID, tenantID, nil
}

// writeAuditRows writes BOTH the tenant-scoped me_audit_log row AND
// the platform-global super_admin_audit_log row in one transaction.
// Mirrors the slice 142/143 dual-write pattern so the slice-124
// unified aggregator surfaces the event consistently.
//
// FAIL CLOSED: any error rolls back the transaction; the caller
// MUST treat a non-nil error as "do not proceed with the action".
func (h *Handler) writeAuditRows(ctx context.Context, action string, actorID, actorTenantID uuid.UUID, payload map[string]any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal audit payload: %w", err)
	}
	beforeBlob := []byte("{}")

	tx, err := h.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin audit tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// me_audit_log row — tenant-scoped to the actor's session tenant
	// so the slice-124 unified aggregator picks it up via the
	// existing kind='me' UNION branch.
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, before, after, subject_module)
		 VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, 'core')`,
		actorTenantID, actorID, action, beforeBlob, payloadBytes,
	); err != nil {
		return fmt.Errorf("insert me_audit_log: %w", err)
	}

	// super_admin_audit_log row — platform-global forensic record.
	// target_user_id is the actor (no other target makes sense for
	// a self-initiated demo-seed click).
	if _, err := tx.Exec(ctx,
		`INSERT INTO super_admin_audit_log
		 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
		 VALUES ($1, $2, $3, $4, $5)`,
		action, actorID, actorID, actorTenantID, payloadBytes,
	); err != nil {
		return fmt.Errorf("insert super_admin_audit_log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit audit tx: %w", err)
	}
	return nil
}

// clientIP extracts the remote IP for rate-limit keying. It delegates to
// internal/api/auth's trusted-proxy resolver (slice 466) so the two
// IP-capture surfaces share ONE implementation: the X-Forwarded-For walk
// is honored only for hops whose connecting peer is inside a configured
// TRUSTED_PROXY_CIDRS entry, and an empty/unset allowlist yields the direct
// TCP peer. This closes the spoofing vector where an attacker on the admin
// network forged X-Forwarded-For to evade the per-IP rate limiter.
func clientIP(r *http.Request) string {
	return authapi.ClientIP(r)
}

// --- in-memory per-IP token bucket ---
//
// One token per bucket, replenished after `window`. Memory grows
// unbounded if attackers spam unique IPs, but the upstream OPA gate
// requires admin role first — so only authenticated admins can
// reach this limiter. v1 is fine; a future slice could swap this
// for a TTL map.

type ipBucketLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	clock   func() time.Time
	buckets map[string]time.Time // ip -> next-allowed
}

func newIPBucketLimiter(window time.Duration) *ipBucketLimiter {
	return &ipBucketLimiter{
		window:  window,
		clock:   time.Now,
		buckets: map[string]time.Time{},
	}
}

// allow returns true when the IP may proceed and records the next
// allowed timestamp. Returns false when the IP is still inside its
// cooldown window.
func (l *ipBucketLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if next, ok := l.buckets[ip]; ok {
		if now.Before(next) {
			return false
		}
	}
	l.buckets[ip] = now.Add(l.window)
	return true
}

// --- helpers ---
