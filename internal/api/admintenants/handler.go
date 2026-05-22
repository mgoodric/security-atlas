// Package admintenants serves the slice-143 create-tenant HTTP surface.
//
// Two routes:
//
//	GET    /v1/admin/tenants                 -- list every tenant row
//	POST   /v1/admin/tenants                 -- create tenant (super_admin-gated)
//
// Authority gate: super_admin only (slice 143 P0-CT-1). The handler
// reads `jwtmw.FromContext().SuperAdmin` as the load-bearing check;
// the slice-035 OPA middleware via policies/authz/super_admin.rego is
// the second leg (the rego file extends the slice-142 super_admin
// management surface to admit the `tenants` resource segment).
//
// LOAD-BEARING DESIGN — write path uses the BYPASSRLS auth pool.
// The new tenant_id is, by definition, NOT the actor's session
// tenant. The slice-002 four-policy RLS on `tenants` (slice 144)
// would block an `atlas_app` INSERT keyed on a row whose `id` does
// not equal `current_tenant_matches`. The auth pool (slice 198 D3
// pattern) bypasses RLS and the handler enforces the super_admin
// gate at the application layer. Mirrors the slice-198 bootstrap
// branch's transaction shape exactly.
//
// LOAD-BEARING DESIGN — atomicity. One BYPASSRLS transaction wraps:
//
//  1. INSERT tenants (the new identity row, with slug + creator).
//  2. INSERT scope_dimensions (one builtin `environment` dimension).
//  3. INSERT scope_cells (one default "All" cell with environment=prod).
//  4. INSERT users (only if creator_joins_as='admin', anchoring the
//     actor's existing OIDC identity to the new tenant).
//  5. INSERT user_roles (only if creator_joins_as='admin', granting
//     'admin' role for the new tenant).
//  6. INSERT super_admin_audit_log (action='tenant_create',
//     platform-global forensic record).
//  7. INSERT me_audit_log (action='tenant_create', tenant-scoped to
//     the actor's session tenant — picked up by the slice-124
//     unified aggregator via the existing kind='me' UNION branch).
//
// Partial state on failure is impossible — any error rolls back the
// entire transaction (P0-CT-3).
//
// LOAD-BEARING DESIGN — soft rate limit. Maximum 100 tenants per
// super_admin per rolling 24h window (P0-CT-2). Enforced inside the
// transaction via `SELECT count(*) FROM super_admin_audit_log
// WHERE actor_user_id = $actor AND action = 'tenant_create' AND
// occurred_at > now() - interval '24 hours'`. 429 Too Many Requests
// with a Retry-After header. The window is rolling, not calendar-
// day; the Retry-After value is conservative — the lower bound on
// "when does the oldest counted row age out?".
//
// Anti-criteria honored (P0-CT-*):
//
//   - P0-CT-1: strict slug regex `^[a-z0-9][a-z0-9-]{0,62}$` enforced
//     at the handler layer; the schema's UNIQUE index is defense-in-
//     depth.
//   - P0-CT-2: soft rate-limit max 100 tenants / super_admin / 24h.
//   - P0-CT-3: atomic transaction (above).
//   - P0-CT-4: NO DELETE route. The List + Create are the only two
//     verbs this handler exposes.
//   - P0-CT-5: NO bulk import; POST takes exactly one body.
//   - P0-CT-6: integration test fixtures use neutral `test-*` slugs
//     (no vendor-prefixed tokens).
package admintenants

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// auditActionCreate is the value written to both
	// super_admin_audit_log.action and me_audit_log.action on a
	// successful tenant create. Both CHECK constraints admit this
	// value via migration 20260522000000_tenants_slug_create_flow.sql.
	auditActionCreate = "tenant_create"

	// pgUniqueViolation is the SQLSTATE for unique_violation. Returned
	// when a tenant create races another caller on a slug or when a
	// caller resubmits a slug that already exists. Mapped to 409.
	pgUniqueViolation = "23505"

	// DefaultRateLimitPerDay caps tenant creates per super_admin per
	// rolling 24h window. Mirrors the slice-doc 143 P0-CT-2 value. The
	// limit is intentionally generous — a vCISO with 30 clients tops
	// out at the 1:30 ratio for the first onboarding sweep, well
	// inside the cap.
	DefaultRateLimitPerDay = 100

	// rateLimitWindow is the rolling window for the rate-limit count.
	rateLimitWindow = 24 * time.Hour

	// slugMaxLen is the slug regex's upper bound (63 chars total =
	// leading char + up to 62 trailing chars). Enforced both at the
	// regex and as a defense-in-depth length check.
	slugMaxLen = 63

	// nameMaxLen mirrors slice-144's application-side cap on
	// tenants.name (64 UTF-8 bytes). The handler trims first, then
	// validates length.
	nameMaxLen = 64

	// creatorJoinsAsAdmin / creatorJoinsAsNone are the two allowed
	// values for the creator_joins_as field.
	creatorJoinsAsAdmin = "admin"
	creatorJoinsAsNone  = "none"

	// defaultDimensionName is the canonical builtin first scope
	// dimension. Mirrors deploy/docker/bootstrap/seed.sql.
	defaultDimensionName = "environment"

	// defaultScopeCellLabel is the human-readable label rendered for
	// the new tenant's seed cell.
	defaultScopeCellLabel = "All"

	// defaultScopeCellEnv is the value the seed cell pins for the
	// `environment` dimension.
	defaultScopeCellEnv = "prod"

	// adminRoleGrantedBy is the granted_by audit string the user_roles
	// row carries when the creator joins as admin. Mirrors the
	// slice-198 bootstrap pattern.
	adminRoleGrantedBy = "system:tenant_create"
)

// slugPattern is the canonical slug shape per slice 143 P0-CT-1:
//
//	^[a-z0-9][a-z0-9-]{0,62}$
//
// Lower-case ASCII + digits + hyphen. Total length 1-63 chars.
// Cannot start with a hyphen. Sufficient for URL-safety + DNS-label
// compatibility (RFC 1035 §2.3.1 imposes the same alphabet for
// labels, with slightly stricter 63-char total).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Handler bundles the slice-143 routes. Holds two pool references:
//
//   - `pool` is the RLS-bound atlas_app pool. Used by the GET list
//     handler (a super_admin reads all rows; the handler issues a
//     no-RLS read via the auth pool today, but the list semantics
//     stay simple — see ListRows).
//   - `authPool` is the BYPASSRLS atlas_migrate pool. Used by the
//     POST create handler for the cross-tenant transaction
//     (slice-198 D3 pattern).
type Handler struct {
	pool     *pgxpool.Pool
	authPool *pgxpool.Pool
	clock    func() time.Time
	limit    int
}

// New constructs a Handler. authPool MAY be nil — in that case POST
// returns 503 Service Unavailable (the deployment is misconfigured;
// the slice-198 NewStoreWithAuthPool follows the same pattern).
func New(pool, authPool *pgxpool.Pool) *Handler {
	return &Handler{
		pool:     pool,
		authPool: authPool,
		clock:    time.Now,
		limit:    DefaultRateLimitPerDay,
	}
}

// WithLimit overrides the rate-limit cap. Test-only.
func (h *Handler) WithLimit(n int) *Handler {
	h.limit = n
	return h
}

// WithClock overrides the clock. Test-only.
func (h *Handler) WithClock(fn func() time.Time) *Handler {
	h.clock = fn
	return h
}

// --- wire types ---

// tenantWire is the JSON shape of one tenant row.
type tenantWire struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Slug              *string   `json:"slug,omitempty"`
	IsBootstrapTenant bool      `json:"is_bootstrap_tenant"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedByUserID   *string   `json:"created_by_user_id,omitempty"`
}

// listResponse is GET /v1/admin/tenants.
type listResponse struct {
	Items []tenantWire `json:"items"`
}

// createRequest is POST /v1/admin/tenants.
//
// `creator_joins_as` is optional and defaults to "none". When "admin"
// the handler also writes a `users` row + `user_roles` row anchoring
// the actor's existing OIDC identity to the new tenant.
type createRequest struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	CreatorJoinsAs string `json:"creator_joins_as,omitempty"`
}

// createResponse is the result of POST /v1/admin/tenants.
//
// `creator_admin_user_id` is non-nil only when creator_joins_as was
// "admin"; it is the new `users.id` for the actor's row inside the
// new tenant.
type createResponse struct {
	Tenant             tenantWire `json:"tenant"`
	CreatorAdminUserID *string    `json:"creator_admin_user_id,omitempty"`
}

// --- handlers ---

// List handles GET /v1/admin/tenants.
//
// Returns every tenant row. Reads via the BYPASSRLS auth pool because
// `tenants` is FORCE RLS and the caller's session tenant only sees
// its own row through the RLS-bound atlas_app pool. The handler is
// super_admin-gated; the auth pool read is safe.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdmin(w, r) {
		return
	}
	if h.authPool == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant management unavailable: no auth pool")
		return
	}

	rows, err := h.authPool.Query(r.Context(), `
		SELECT id, name, slug, is_bootstrap_tenant, created_at, created_by_user_id
		FROM tenants
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tenants: "+err.Error())
		return
	}
	defer rows.Close()

	items := make([]tenantWire, 0)
	for rows.Next() {
		var (
			id           uuid.UUID
			name         string
			slug         *string
			isBootstrap  bool
			createdAt    time.Time
			createdByRaw *uuid.UUID
		)
		if err := rows.Scan(&id, &name, &slug, &isBootstrap, &createdAt, &createdByRaw); err != nil {
			writeError(w, http.StatusInternalServerError, "scan tenant: "+err.Error())
			return
		}
		var createdByStr *string
		if createdByRaw != nil {
			s := createdByRaw.String()
			createdByStr = &s
		}
		items = append(items, tenantWire{
			ID:                id.String(),
			Name:              name,
			Slug:              slug,
			IsBootstrapTenant: isBootstrap,
			CreatedAt:         createdAt,
			CreatedByUserID:   createdByStr,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "iterate tenants: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, listResponse{Items: items})
}

// Create handles POST /v1/admin/tenants.
//
// Body: {"name": "...", "slug": "...", "creator_joins_as": "admin"|"none"}.
//
// Validation:
//   - name: non-empty after trim, ≤ 64 bytes
//   - slug: matches `^[a-z0-9][a-z0-9-]{0,62}$`
//   - creator_joins_as: "admin" or "none" (default "none")
//
// Atomicity: one BYPASSRLS transaction wraps every write. Partial
// state on failure is impossible (P0-CT-3).
//
// Rate-limit: 100 tenants/super_admin/24h enforced via
// super_admin_audit_log count over the rolling window (P0-CT-2). 429
// with Retry-After when exceeded.
//
// Returns 200 OK with the new tenant payload on success.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdmin(w, r) {
		return
	}
	if h.authPool == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant management unavailable: no auth pool")
		return
	}

	var req createRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate name. Trim whitespace; reject empty; cap length.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > nameMaxLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name exceeds %d-byte cap", nameMaxLen))
		return
	}

	// Validate slug. Trim whitespace; require non-empty; require regex match.
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	if len(slug) > slugMaxLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("slug exceeds %d-byte cap", slugMaxLen))
		return
	}
	if !slugPattern.MatchString(slug) {
		writeError(w, http.StatusBadRequest, "slug must match ^[a-z0-9][a-z0-9-]{0,62}$")
		return
	}

	// Validate creator_joins_as.
	joinsAs := strings.TrimSpace(req.CreatorJoinsAs)
	if joinsAs == "" {
		joinsAs = creatorJoinsAsNone
	}
	if joinsAs != creatorJoinsAsAdmin && joinsAs != creatorJoinsAsNone {
		writeError(w, http.StatusBadRequest, "creator_joins_as must be 'admin' or 'none'")
		return
	}

	// Resolve actor identity.
	actorID := actorFromContext(r.Context())
	if actorID == uuid.Nil {
		writeError(w, http.StatusInternalServerError, "actor user_id not on context")
		return
	}
	actorTenantID, terr := actorTenantFromContext(r.Context())
	if terr != nil {
		writeError(w, http.StatusInternalServerError, terr.Error())
		return
	}

	// Read the actor's OIDC identity from their session-tenant users
	// row. Needed when creator_joins_as='admin' so the new tenant's
	// users row carries the same (idp_issuer, idp_subject) pair —
	// the slice-198 + slice-192 user_resolver enumerates memberships
	// by exactly that pair, so multi-tenant login works for the
	// actor across both their session tenant and the new tenant.
	//
	// Reads via the auth pool (no RLS) because we are about to use
	// the auth pool for the transaction anyway. The session tenant
	// users row is keyed on (id) which is globally unique even
	// without RLS scoping.
	var actorIdpIssuer, actorIdpSubject, actorEmail, actorDisplayName string
	if joinsAs == creatorJoinsAsAdmin {
		err := h.authPool.QueryRow(r.Context(),
			`SELECT idp_issuer, idp_subject, email, display_name
			 FROM users WHERE id = $1`,
			actorID,
		).Scan(&actorIdpIssuer, &actorIdpSubject, &actorEmail, &actorDisplayName)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError,
				"actor users row not found; cannot join new tenant as admin")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError,
				"read actor identity: "+err.Error())
			return
		}
	}

	// Synthesize the new tenant ID up front so audit-log rows reference it.
	tenantID := uuid.New()

	// The transaction.
	var (
		creatorAdminUserID *uuid.UUID
		uniqueViolation    bool
		uniqueColumn       string
	)
	tx, err := h.authPool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx: "+err.Error())
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// 0. Per-actor advisory lock — serialises concurrent create-tenant
	//    requests from the same actor. The rate-limit count below
	//    reads super_admin_audit_log inside the transaction; without
	//    serialisation, N concurrent transactions all see count=K
	//    and all proceed (the limit becomes K + N - 1 instead of K).
	//
	//    Lock key derivation: 0x143... prefix (slice-143-unique) +
	//    the actor's UUID upper 64 bits. Distinct from slice 142's
	//    0x142142142142 key (the slice-142 demote lock). Future
	//    slices needing per-actor advisory locks must pick a distinct
	//    high-bit prefix to avoid cross-feature blocking.
	//
	//    Lock is auto-released at transaction commit (xact_lock).
	//    Mirrors the slice 142 D1 pattern.
	actorLockKey := actorAdvisoryKey(actorID)
	if _, err := tx.Exec(r.Context(),
		`SELECT pg_advisory_xact_lock($1)`, actorLockKey,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "acquire actor lock: "+err.Error())
		return
	}

	// 1. Rate-limit gate (inside the transaction; serialised against
	//    concurrent creates from the same actor via the advisory lock
	//    above).
	var rateCount int
	rateCutoff := h.clock().Add(-rateLimitWindow)
	if err := tx.QueryRow(r.Context(),
		`SELECT count(*) FROM super_admin_audit_log
		 WHERE actor_user_id = $1 AND action = $2 AND occurred_at > $3`,
		actorID, auditActionCreate, rateCutoff,
	).Scan(&rateCount); err != nil {
		writeError(w, http.StatusInternalServerError, "rate check: "+err.Error())
		return
	}
	if rateCount >= h.limit {
		// Conservative Retry-After: 24h is the upper bound on when
		// the earliest counted row ages out. The handler does NOT
		// compute the exact "earliest row's age + 24h" because the
		// query would require an additional round-trip and the
		// extra precision is not worth the cost — operators hitting
		// this cap are misusing the surface and a 24h cooldown
		// signals that clearly.
		// Fall through to the error response below (don't commit).
		_ = tx.Rollback(r.Context())
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rateLimitWindow.Seconds())))
		writeError(w, http.StatusTooManyRequests,
			fmt.Sprintf("rate limit exceeded: max %d tenants per super_admin per 24h", h.limit))
		return
	}

	// 2. INSERT tenants. The slice-144 LOWER(name) UNIQUE index and
	//    the slice-143 idx_tenants_slug_unique index fire on conflict.
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO tenants (id, name, slug, is_bootstrap_tenant, created_by_user_id)
		 VALUES ($1, $2, $3, false, $4)`,
		tenantID, name, slug, actorID,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			uniqueViolation = true
			uniqueColumn = pgErr.ConstraintName
		} else {
			writeError(w, http.StatusInternalServerError, "insert tenant: "+err.Error())
			return
		}
	}
	if uniqueViolation {
		// Map both indexes to user-facing 409 with a clear column hint.
		_ = tx.Rollback(r.Context())
		switch uniqueColumn {
		case "idx_tenants_slug_unique":
			writeError(w, http.StatusConflict, "slug already in use")
		case "idx_tenants_lower_name":
			writeError(w, http.StatusConflict, "name already in use (case-insensitive)")
		default:
			writeError(w, http.StatusConflict, "tenant conflict on unique constraint: "+uniqueColumn)
		}
		return
	}

	// 3. INSERT scope_dimensions: one builtin `environment` dimension.
	//    Mirrors deploy/docker/bootstrap/seed.sql. The dimension is
	//    required so the seed scope_cell below has a dimension to
	//    range over (the slice-002 scope model requires every cell
	//    to declare values for the tenant's dimensions).
	dimensionID := uuid.New()
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO scope_dimensions
		 (id, tenant_id, name, value_type, allowed_values, is_required, is_builtin)
		 VALUES ($1, $2, $3, 'string', $4::jsonb, FALSE, TRUE)`,
		dimensionID, tenantID, defaultDimensionName,
		`["prod", "staging", "dev"]`,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert scope_dimension: "+err.Error())
		return
	}

	// 4. INSERT scope_cells: the "All" cell, environment=prod.
	//    dimensions_hash is the SHA-256 of the canonical JSON
	//    `{"environment":"prod"}` — pre-computed so the UNIQUE
	//    (tenant_id, dimensions_hash) constraint is satisfied
	//    deterministically. Mirrors seed.sql.
	cellID := uuid.New()
	dimensionsCanonical := `{"environment":"prod"}`
	hash := sha256.Sum256([]byte(dimensionsCanonical))
	dimensionsHash := hex.EncodeToString(hash[:])
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO scope_cells
		 (id, tenant_id, label, dimensions, dimensions_hash)
		 VALUES ($1, $2, $3, $4::jsonb, $5)`,
		cellID, tenantID, defaultScopeCellLabel,
		`{"environment": "prod"}`, dimensionsHash,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert scope_cell: "+err.Error())
		return
	}

	// 5. (conditional) INSERT users + user_roles when creator_joins_as='admin'.
	if joinsAs == creatorJoinsAsAdmin {
		newUserID := uuid.New()
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
			 VALUES ($1, $2, $3, $4, 'active', $5, $6)`,
			newUserID, tenantID, actorEmail, actorDisplayName,
			actorIdpIssuer, actorIdpSubject,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "insert creator users row: "+err.Error())
			return
		}
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
			 VALUES ($1, $2, 'admin', $3)`,
			tenantID, newUserID.String(), adminRoleGrantedBy,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "insert creator user_roles row: "+err.Error())
			return
		}
		creatorAdminUserID = &newUserID
	}

	// 6. INSERT super_admin_audit_log (platform-global forensic record).
	payload := mustMarshal(map[string]any{
		"new_tenant_id":    tenantID.String(),
		"name":             name,
		"slug":             slug,
		"creator_joins_as": joinsAs,
	})
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO super_admin_audit_log
		 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
		 VALUES ($1, $2, $3, $4, $5)`,
		auditActionCreate,
		actorID, // target_user_id: the actor (no other target makes sense for create-tenant)
		actorID,
		actorTenantID,
		payload,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert super_admin_audit_log: "+err.Error())
		return
	}

	// 7. INSERT me_audit_log (tenant-scoped; surfaces via slice-124
	//    unified aggregator's existing kind='me' branch).
	beforeBlob := mustMarshal(map[string]any{})
	afterBlob := mustMarshal(map[string]any{
		"new_tenant_id":    tenantID.String(),
		"name":             name,
		"slug":             slug,
		"creator_joins_as": joinsAs,
	})
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO me_audit_log (tenant_id, user_id, action, before, after, subject_module)
		 VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, 'core')`,
		actorTenantID, actorID, auditActionCreate, beforeBlob, afterBlob,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "insert me_audit_log: "+err.Error())
		return
	}

	// Commit.
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "commit: "+err.Error())
		return
	}

	// External audit sink fanout (slice 126).
	sinkPayload := mustMarshal(map[string]any{
		"new_tenant_id":    tenantID.String(),
		"name":             name,
		"slug":             slug,
		"creator_joins_as": joinsAs,
	})
	sink.EmitDefault(r.Context(), unifiedlog.Entry{
		OccurredAt:    h.clock().UTC(),
		ActorID:       actorID.String(),
		TenantID:      actorTenantID,
		Kind:          unifiedlog.KindMe,
		TargetType:    "tenant",
		TargetID:      tenantID.String(),
		Action:        auditActionCreate,
		RowID:         uuid.New(),
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   sinkPayload,
	})

	slugCopy := slug
	resp := createResponse{
		Tenant: tenantWire{
			ID:                tenantID.String(),
			Name:              name,
			Slug:              &slugCopy,
			IsBootstrapTenant: false,
			CreatedAt:         h.clock().UTC(),
			CreatedByUserID:   stringPtr(actorID.String()),
		},
	}
	if creatorAdminUserID != nil {
		s := creatorAdminUserID.String()
		resp.CreatorAdminUserID = &s
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- helpers ---

// requireSuperAdmin returns true when the caller carries the
// super_admin bit. Mirrors the slice-142 pattern.
func requireSuperAdmin(w http.ResponseWriter, r *http.Request) bool {
	claims := jwtmw.FromContext(r.Context())
	if claims != nil && claims.SuperAdmin {
		return true
	}
	writeError(w, http.StatusForbidden, "super_admin required")
	return false
}

// actorFromContext returns the caller's user_id from the JWT subject
// claim. Super_admin is JWT-only at v1, so the JWT subject is the
// authoritative source.
func actorFromContext(ctx context.Context) uuid.UUID {
	if claims := jwtmw.FromContext(ctx); claims != nil {
		if u, err := uuid.Parse(claims.Subject); err == nil {
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
		panic(fmt.Sprintf("admintenants: json marshal: %v", err))
	}
	return b
}

func stringPtr(s string) *string { return &s }

// actorAdvisoryKey derives a per-actor BIGINT key for the slice-143
// per-actor advisory lock. The high bit of the key is the slice-143-
// stable prefix (0x143 << 48); the low bits are the upper 48 bits of
// the actor's UUID. The result is deterministic per actor and won't
// collide with the slice-142 advisory lock (0x142142142142) because
// the high prefix differs.
//
// Why upper 48 bits and not lower: UUIDv4's most-uniform entropy lives
// in the low 48 bits, but using the high 48 bits gives us a stable
// mapping that doesn't depend on UUID version. For test fixtures
// generated via uuid.New() (v4), both halves are equally uniform.
//
// Returns int64 because Postgres `pg_advisory_xact_lock(BIGINT)` is
// signed; we mask away the sign bit so the result fits in int63.
func actorAdvisoryKey(actorID uuid.UUID) int64 {
	const slice143Prefix int64 = 0x0143000000000000
	bytes := actorID
	// Take the upper 6 bytes of the UUID for the actor component.
	var actorComponent int64
	for i := 0; i < 6; i++ {
		actorComponent = (actorComponent << 8) | int64(bytes[i])
	}
	// Mask the actor component into the low 48 bits to leave the
	// prefix intact.
	return slice143Prefix | (actorComponent & 0x0000ffffffffffff)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
