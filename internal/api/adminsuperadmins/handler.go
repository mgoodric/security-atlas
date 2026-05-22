// Package adminsuperadmins serves the slice-142 super_admin management
// HTTP surface.
//
// Three routes:
//
//	GET    /v1/admin/super-admins              -- list every super_admin row
//	POST   /v1/admin/super-admins              -- grant super_admin (super_admin-gated)
//	DELETE /v1/admin/super-admins/{user_id}    -- demote super_admin (super_admin-gated)
//
// Authority gate: super_admin only (slice 142 P0). The handler reads
// `jwtmw.FromContext().SuperAdmin` as the load-bearing check; the
// slice-035 OPA middleware via policies/authz/super_admin.rego is the
// second leg.
//
// LOAD-BEARING DESIGN: last-super_admin safety rail. The DELETE handler
// wraps the demote in a single transaction that:
//
//  1. Acquires `pg_advisory_xact_lock(0x142142142142)` — a slice-142
//     stable BIGINT advisory lock that serialises concurrent demotes so
//     two callers cannot each see count=2 and proceed to DELETE both.
//     Advisory locks were chosen over `SELECT ... FOR UPDATE` because
//     atlas_app lacks the Postgres UPDATE privilege required for
//     row-level locking on super_admins (the table is functionally
//     append+delete only — no UPDATE writes). See decisions log D1.
//  2. Reads `SELECT count(*) FROM super_admins`. With the advisory
//     lock held this is safe — concurrent demotes block above.
//  3. Returns 409 Conflict when count == 1 (the only row IS the target).
//  4. DELETEs the target row.
//  5. INSERTs a super_admin_audit_log row.
//  6. INSERTs a me_audit_log row tagged with the actor's session tenant.
//  7. Commits — the advisory lock is auto-released.
//
// The slice-142 integration test (AC-10) asserts BOTH the single-demote
// 409 (count==1 case) AND the concurrent-demote serialisation
// (count==2 → exactly one DELETE succeeds + one 409).
//
// Anti-criteria honored (P0-SA-*):
//
//   - P0-SA-1: last-super_admin safety rail (above).
//   - P0-SA-2: grant + demote each write super_admin_audit_log AND
//     me_audit_log SAME-TRANSACTION; no out-of-band writes.
//   - P0-SA-3: NO super_admin self-add to user_tenants. The grant
//     handler does NOT touch user_tenants; the granted identity must
//     already have a user_roles row in some tenant via the standard
//     /v1/admin/users surface (slice 062) to acquire tenant-write
//     authority. Super_admin alone does NOT confer tenant write.
//   - P0-SA-4: NO `expires_at` parameter on POST. v1 is permanent
//     grants only.
//   - P0-SA-5: NO vendor-prefixed test fixture tokens.
package adminsuperadmins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// auditActionGrant is the me_audit_log.action + the
	// super_admin_audit_log.action value written on a successful grant.
	// CHECK constraints on both tables admit this value (migration
	// 20260521030000_super_admins_full.sql).
	auditActionGrant = "super_admin_grant"

	// auditActionRevoke is the me_audit_log.action +
	// super_admin_audit_log.action value written on a successful demote.
	auditActionRevoke = "super_admin_revoke"

	// grantViaManual is the super_admins.granted_via value for runtime
	// grants. Slice 142 migration extends the CHECK constraint to admit
	// this value (alongside slice 198's 'bootstrap_first_install').
	grantViaManual = "manual_grant"

	// pgUniqueViolation is the SQLSTATE for unique_violation. Returned
	// when a grant targets an already-super_admin (already in the
	// table).
	pgUniqueViolation = "23505"
)

// Handler bundles the slice-142 routes.
type Handler struct {
	pool *pgxpool.Pool
}

// New constructs a Handler over a *pgxpool.Pool. The handler uses the
// RLS-bound `atlas_app` pool — super_admins itself is NOT under RLS
// (slice 198 D3), but the parallel me_audit_log write needs the tenant
// GUC. Single pool keeps the transaction surface simple.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// --- wire types ---

// superAdminWire is the JSON shape of one super_admin row.
type superAdminWire struct {
	UserID      string    `json:"user_id"`
	GrantedAt   time.Time `json:"granted_at"`
	GrantedVia  string    `json:"granted_via"`
	DisplayName *string   `json:"display_name,omitempty"`
	Email       *string   `json:"email,omitempty"`
}

// listResponse is GET /v1/admin/super-admins.
type listResponse struct {
	Items []superAdminWire `json:"items"`
}

// grantRequest is POST /v1/admin/super-admins.
type grantRequest struct {
	UserID string `json:"user_id"`
}

// --- handlers ---

// List handles GET /v1/admin/super-admins.
//
// Returns every super_admin row, joined LEFT to users (under the
// caller's session tenant) so the display_name + email render on the
// management page. Rows whose target_user_id has no users row in the
// session tenant render as `null` for the display fields — this is the
// expected case for a super_admin whose primary tenant is not the
// session tenant.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdmin(w, r) {
		return
	}

	type row struct {
		UserID      uuid.UUID
		GrantedAt   time.Time
		GrantedVia  string
		DisplayName *string
		Email       *string
	}

	var rows []row
	err := h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) error {
		// LEFT JOIN against users under the session tenant's RLS — the
		// users.* columns are NULL when no users row matches under
		// that tenant's RLS policy. That's the expected case for a
		// super_admin whose primary tenant ≠ the session tenant.
		// ORDER BY granted_at ASC mirrors the slice-198 bootstrap
		// expectation that the first-installer appears first.
		queryRows, qErr := tx.Query(ctx, `
			SELECT sa.user_id, sa.granted_at, sa.granted_via,
			       u.display_name, u.email
			FROM super_admins sa
			LEFT JOIN users u ON u.id = sa.user_id
			ORDER BY sa.granted_at ASC, sa.user_id ASC
		`)
		if qErr != nil {
			return fmt.Errorf("query super_admins: %w", qErr)
		}
		defer queryRows.Close()
		for queryRows.Next() {
			var rr row
			if scanErr := queryRows.Scan(&rr.UserID, &rr.GrantedAt, &rr.GrantedVia, &rr.DisplayName, &rr.Email); scanErr != nil {
				return fmt.Errorf("scan super_admin row: %w", scanErr)
			}
			rows = append(rows, rr)
		}
		return queryRows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list super_admins: "+err.Error())
		return
	}

	items := make([]superAdminWire, 0, len(rows))
	for _, rr := range rows {
		items = append(items, superAdminWire{
			UserID:      rr.UserID.String(),
			GrantedAt:   rr.GrantedAt,
			GrantedVia:  rr.GrantedVia,
			DisplayName: rr.DisplayName,
			Email:       rr.Email,
		})
	}
	writeJSON(w, http.StatusOK, listResponse{Items: items})
}

// Grant handles POST /v1/admin/super-admins.
//
// Body: {"user_id": "<uuid>"}. Idempotent — re-granting an already-
// super_admin returns 200 with the existing row (no duplicate audit-
// log write).
func (h *Handler) Grant(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdmin(w, r) {
		return
	}

	var req grantRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	targetID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "user_id must be a UUID")
		return
	}

	actorID := actorFromContext(r.Context())
	if actorID == uuid.Nil {
		// The super_admin gate guarantees a JWT context with a real
		// subject — falling through here indicates a programmer error
		// (missing test-mode subject, broken middleware chain).
		writeError(w, http.StatusInternalServerError, "actor user_id not on context")
		return
	}
	actorTenantID, terr := actorTenantFromContext(r.Context())
	if terr != nil {
		writeError(w, http.StatusInternalServerError, terr.Error())
		return
	}

	var (
		grantedRow superAdminWire
		idempotent bool
	)
	err = h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) error {
		// INSERT with ON CONFLICT DO NOTHING — idempotent re-grant.
		// We then RETURNING the row if inserted; on conflict the row
		// is unchanged + RETURNING is empty, so we re-SELECT.
		var grantedAt time.Time
		var grantedVia string
		insErr := tx.QueryRow(ctx,
			`INSERT INTO super_admins (user_id, granted_via)
			 VALUES ($1, $2)
			 ON CONFLICT (user_id) DO NOTHING
			 RETURNING granted_at, granted_via`,
			targetID, grantViaManual,
		).Scan(&grantedAt, &grantedVia)

		switch {
		case errors.Is(insErr, pgx.ErrNoRows):
			// Conflict path — already a super_admin. Read the existing
			// row + mark idempotent so we skip the audit-log writes.
			idempotent = true
			selErr := tx.QueryRow(ctx,
				`SELECT granted_at, granted_via FROM super_admins WHERE user_id = $1`,
				targetID,
			).Scan(&grantedAt, &grantedVia)
			if selErr != nil {
				return fmt.Errorf("select existing super_admin: %w", selErr)
			}
		case insErr != nil:
			var pgErr *pgconn.PgError
			if errors.As(insErr, &pgErr) && pgErr.Code == pgUniqueViolation {
				idempotent = true
				selErr := tx.QueryRow(ctx,
					`SELECT granted_at, granted_via FROM super_admins WHERE user_id = $1`,
					targetID,
				).Scan(&grantedAt, &grantedVia)
				if selErr != nil {
					return fmt.Errorf("select existing super_admin: %w", selErr)
				}
			} else {
				return fmt.Errorf("insert super_admin: %w", insErr)
			}
		}

		grantedRow = superAdminWire{
			UserID:     targetID.String(),
			GrantedAt:  grantedAt,
			GrantedVia: grantedVia,
		}

		// Skip audit-log writes on the idempotent re-grant — the
		// original grant already wrote them. P0-SA-2 still holds: the
		// non-idempotent path always writes both rows.
		if idempotent {
			return nil
		}

		// 1. super_admin_audit_log (platform-global forensic record).
		payload := mustMarshal(map[string]any{
			"granted_via": grantViaManual,
		})
		if _, err := tx.Exec(ctx,
			`INSERT INTO super_admin_audit_log
			 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
			 VALUES ($1, $2, $3, $4, $5)`,
			auditActionGrant, targetID, actorID, actorTenantID, payload,
		); err != nil {
			return fmt.Errorf("insert super_admin_audit_log: %w", err)
		}

		// 2. me_audit_log (tenant-scoped; flows through slice-124
		//    unified aggregator via the existing kind='me' branch).
		beforeBlob := mustMarshal(map[string]any{})
		afterBlob := mustMarshal(map[string]any{
			"target_user_id": targetID.String(),
			"granted_via":    grantViaManual,
		})
		if err := dbx.New(tx).InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID: pgtype.UUID{Bytes: actorTenantID, Valid: true},
			UserID:   pgtype.UUID{Bytes: actorID, Valid: true},
			Action:   auditActionGrant,
			Before:   beforeBlob,
			After:    afterBlob,
		}); err != nil {
			return fmt.Errorf("insert me_audit_log: %w", err)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "grant super_admin: "+err.Error())
		return
	}

	// External audit sink fanout (slice 126). Skipped on idempotent
	// re-grant — that path produces no new event.
	if !idempotent {
		sinkPayload := mustMarshal(map[string]any{
			"target_user_id": targetID.String(),
			"granted_via":    grantViaManual,
		})
		sink.EmitDefault(r.Context(), unifiedlog.Entry{
			OccurredAt:    time.Now().UTC(),
			ActorID:       actorID.String(),
			TenantID:      actorTenantID,
			Kind:          unifiedlog.KindMe,
			TargetType:    "super_admin",
			TargetID:      targetID.String(),
			Action:        auditActionGrant,
			RowID:         uuid.New(),
			SubjectModule: unifiedlog.SubjectModuleCore,
			PayloadJSON:   sinkPayload,
		})
	}

	writeJSON(w, http.StatusOK, grantedRow)
}

// Demote handles DELETE /v1/admin/super-admins/{user_id}.
//
// Implements the LOAD-BEARING last-super_admin safety rail (P0-SA-1):
//
//  1. SELECT count(*) FROM super_admins FOR UPDATE — serialises
//     concurrent demotes against each other.
//  2. If count == 1, the target IS the last super_admin → 409 Conflict.
//  3. DELETE the target row.
//  4. INSERT super_admin_audit_log row.
//  5. INSERT me_audit_log row.
//  6. Commit.
//
// 404 if the target user_id is not in super_admins.
func (h *Handler) Demote(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdmin(w, r) {
		return
	}

	targetID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "user_id")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "user_id must be a UUID")
		return
	}

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

	type result struct {
		notFound     bool
		lastStanding bool
	}
	var res result
	err = h.inTx(r.Context(), func(ctx context.Context, tx pgx.Tx) error {
		// LOAD-BEARING: serialise concurrent demotes via an advisory
		// transaction lock keyed on a slice-142-stable bigint. The
		// `pg_advisory_xact_lock(BIGINT)` call blocks until the
		// lock is acquired; the lock is automatically released at
		// transaction commit. This is the slice-198 D1 candidate-#2
		// pattern (advisory lock + count recheck) applied here
		// because the table-row alternative `SELECT ... FOR UPDATE`
		// requires UPDATE privilege on super_admins which atlas_app
		// intentionally does NOT hold (the table is functionally
		// append+delete only — no UPDATE writes).
		//
		// The key (142_142_142_142_142_1) is a slice-142-unique
		// constant; future slices that need their own advisory locks
		// must pick a distinct key to avoid cross-feature blocking.
		if _, lErr := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(0x142142142142)`,
		); lErr != nil {
			return fmt.Errorf("acquire super_admin lock: %w", lErr)
		}

		// With the advisory lock held, count(*) is safe — concurrent
		// demotes block above on the same lock, so they cannot read a
		// stale count between our SELECT and our DELETE.
		var count int
		if cErr := tx.QueryRow(ctx,
			`SELECT count(*) FROM super_admins`,
		).Scan(&count); cErr != nil {
			return fmt.Errorf("count super_admins: %w", cErr)
		}

		// Verify target exists. We do this AFTER FOR UPDATE so we
		// already hold the lock; a separate read would race against
		// the count.
		var exists bool
		if xErr := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM super_admins WHERE user_id = $1)`,
			targetID,
		).Scan(&exists); xErr != nil {
			return fmt.Errorf("check super_admin exists: %w", xErr)
		}
		if !exists {
			res.notFound = true
			return nil
		}

		// Last-super_admin guard. When count == 1 the only row IS the
		// target (we just confirmed exists==true). 409.
		if count <= 1 {
			res.lastStanding = true
			return nil
		}

		// DELETE the target row.
		if _, dErr := tx.Exec(ctx,
			`DELETE FROM super_admins WHERE user_id = $1`,
			targetID,
		); dErr != nil {
			return fmt.Errorf("delete super_admin: %w", dErr)
		}

		// super_admin_audit_log row.
		payload := mustMarshal(map[string]any{})
		if _, aErr := tx.Exec(ctx,
			`INSERT INTO super_admin_audit_log
			 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
			 VALUES ($1, $2, $3, $4, $5)`,
			auditActionRevoke, targetID, actorID, actorTenantID, payload,
		); aErr != nil {
			return fmt.Errorf("insert super_admin_audit_log: %w", aErr)
		}

		// me_audit_log row.
		beforeBlob := mustMarshal(map[string]any{
			"target_user_id": targetID.String(),
		})
		afterBlob := mustMarshal(map[string]any{})
		if mErr := dbx.New(tx).InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID: pgtype.UUID{Bytes: actorTenantID, Valid: true},
			UserID:   pgtype.UUID{Bytes: actorID, Valid: true},
			Action:   auditActionRevoke,
			Before:   beforeBlob,
			After:    afterBlob,
		}); mErr != nil {
			return fmt.Errorf("insert me_audit_log: %w", mErr)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "demote super_admin: "+err.Error())
		return
	}
	if res.notFound {
		writeError(w, http.StatusNotFound, "super_admin not found")
		return
	}
	if res.lastStanding {
		writeError(w, http.StatusConflict, "Cannot demote the last super_admin")
		return
	}

	// External audit sink fanout (slice 126).
	sinkPayload := mustMarshal(map[string]any{
		"target_user_id": targetID.String(),
	})
	sink.EmitDefault(r.Context(), unifiedlog.Entry{
		OccurredAt:    time.Now().UTC(),
		ActorID:       actorID.String(),
		TenantID:      actorTenantID,
		Kind:          unifiedlog.KindMe,
		TargetType:    "super_admin",
		TargetID:      targetID.String(),
		Action:        auditActionRevoke,
		RowID:         uuid.New(),
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   sinkPayload,
	})

	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func (h *Handler) inTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("apply tenant: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// requireSuperAdmin returns true when the caller carries the
// super_admin bit. Defense-in-depth on top of the OPA policy gate
// (policies/authz/super_admin.rego).
//
// The only authority that grants super_admin is the verified JWT's
// `atlas:super_admin` claim — that bit can be set ONLY by the slice-
// 192 user_resolver looking up the slice-198 super_admins table. The
// legacy bearer path (cred.IsAdmin via the slice-014 fixed-token
// admin) is a TENANT-scoped admin, NOT super_admin, and is rejected
// here to honour P0-SA-3 (no implicit platform-global escalation from
// per-tenant admin credentials).
func requireSuperAdmin(w http.ResponseWriter, r *http.Request) bool {
	claims := jwtmw.FromContext(r.Context())
	if claims != nil && claims.SuperAdmin {
		return true
	}
	writeError(w, http.StatusForbidden, "super_admin required")
	return false
}

// actorFromContext returns the caller's user_id from the JWT subject
// claim. Super_admin is JWT-only at v1 (requireSuperAdmin rejects the
// legacy bearer path), so the JWT subject is the authoritative source.
// Returns uuid.Nil only on a non-UUID subject — an integration
// invariant the super_admin gate above should have caught.
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
		// Should be unreachable for the shapes we feed in (no
		// channels/funcs); panicking surfaces the programmer error
		// instead of writing malformed JSON.
		panic(fmt.Sprintf("adminsuperadmins: json marshal: %v", err))
	}
	return b
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
