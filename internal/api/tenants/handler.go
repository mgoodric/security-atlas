// Package tenants serves the slice-144 HTTP API for the tenants
// primitive. v1 ships exactly one mutator:
//
//	PATCH /v1/tenants/{id}   rename a tenant
//
// The endpoint is gated on (per-tenant admin) OR (super_admin per
// the JWT claim). cred.IsAdmin is the slice-034 admin flag carried
// on the legacy credential path; jwtmw.FromContext().SuperAdmin is
// the slice-187 claim carried on the OAuth path. Either grants
// rename authority on the caller's CURRENT tenant; cross-tenant
// rename is denied by the schema layer (RLS) even if the role gate
// were bypassed.
//
// What this slice does NOT ship (per the slice 144 spec):
//
//   - Slug rename — out of scope (affects URLs).
//   - Tenant logo / branding / metadata — out of scope.
//   - Tenant rename history rendering — audit-log captures the
//     trail; no dedicated UI to view rename history at v1.
//   - Per-tenant URL routing changes — out of scope.
package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-144 routes. The store is a *pgxpool.Pool
// because every write opens its own transaction (UPDATE + audit-log
// row are atomic via tenancy.ApplyTenant).
type Handler struct {
	pool *pgxpool.Pool
}

// New constructs a tenants Handler over a *pgxpool.Pool.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// Wire types ---------------------------------------------------------

// patchTenantRequest is the request body for PATCH /v1/tenants/{id}.
//
// Each field is a pointer so the absence of the field can be distinguished
// from an explicit value. A PATCH may carry name, bundle_gate_mode, or both;
// a PATCH that carries neither is a 400 (nothing to change).
//
//   - Name (slice 144): rename the tenant.
//   - BundleGateMode (slice 608): set the control-bundle upload gate policy
//     (strict | advisory | mandatory_tests).
type patchTenantRequest struct {
	Name           *string `json:"name"`
	BundleGateMode *string `json:"bundle_gate_mode"`
}

// tenantWire is the JSON shape of a tenant row on the wire.
type tenantWire struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IsBootstrapTenant bool   `json:"is_bootstrap_tenant"`
	// BundleGateMode is the slice-608 per-tenant control-bundle upload gate
	// policy: strict (default) | advisory | mandatory_tests. Settable via the
	// PATCH body alongside (or instead of) name.
	BundleGateMode string    `json:"bundle_gate_mode"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Validation constants -----------------------------------------------

const (
	// maxNameLenBytes caps the rename input. 64 UTF-8 bytes mirrors
	// the slice-024 vendor.name + slice-022 policies.name caps; the
	// slice-192 tenant-switcher dropdown is laid out for ~32 visible
	// characters and 64 bytes accommodates emoji / non-Latin scripts
	// without truncation surprises.
	maxNameLenBytes = 64

	// auditActionTenantRename is the me_audit_log.action value
	// written on a successful rename. CHECK constraint extended in
	// migration 20260521010000_tenants_rename.sql.
	auditActionTenantRename = "tenant_rename"

	// auditActionTenantGatePolicy is the me_audit_log.action value written on
	// a successful control-bundle gate-policy change (slice 608). CHECK
	// constraint extended in migration 20260608030000_tenant_bundle_gate_mode.sql.
	auditActionTenantGatePolicy = "tenant_gate_policy_update"
)

// PatchTenant handles PATCH /v1/tenants/{id}.
//
//  1. Parse + validate the URL id.
//  2. Resolve caller's tenant context and authority. The caller's
//     tenant_id MUST equal the URL id (RLS enforces; this is a
//     second leg for clearer 4xx vs 5xx discrimination).
//  3. Decode body; normalize + validate name.
//  4. Open a transaction under the caller's tenant GUC; UPDATE
//     tenants; INSERT me_audit_log with action='tenant_rename';
//     commit.
//  5. Emit to the external audit sink (slice 126).
//  6. Return the post-update wire shape.
func (h *Handler) PatchTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}

	// Caller's tenant scope. The slice-033 tenancymw lifts the
	// credential's TenantID onto the request context; we resolve
	// here and require an exact match with the URL id. This is
	// the "no cross-tenant rename" leg (P0 anti-criterion); RLS
	// is the load-bearing second leg.
	callerTenantStr, terr := tenancy.TenantFromContext(ctx)
	if terr != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	callerTenantID, perr := uuid.Parse(callerTenantStr)
	if perr != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in context")
		return
	}
	if callerTenantID != id {
		// Same 403 body shape for "doesn't exist" and "no access"
		// to avoid the membership-enumeration timing leak.
		httpresp.WriteError(w, http.StatusForbidden, "tenant not accessible")
		return
	}

	// Authority gate. Per-tenant admin (cred.IsAdmin on the slice-034
	// path) OR super_admin (claims.SuperAdmin on the slice-190 JWT
	// path). Either grants rename authority on the caller's CURRENT
	// tenant.
	if !h.callerCanRename(ctx) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or super_admin required")
		return
	}

	var req patchTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// A PATCH must carry at least one mutator. Name and bundle_gate_mode are
	// independently optional; neither present is a 400 (nothing to change).
	if req.Name == nil && req.BundleGateMode == nil {
		httpresp.WriteError(w, http.StatusBadRequest, "name or bundle_gate_mode is required")
		return
	}

	var normalizedName string
	if req.Name != nil {
		n, verr := normalizeName(*req.Name)
		if verr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, verr.Error())
			return
		}
		normalizedName = n
	}

	var gateMode string
	if req.BundleGateMode != nil {
		m, ok := controls.ParseGateMode(*req.BundleGateMode)
		if !ok || *req.BundleGateMode == "" {
			// Empty string is not a valid explicit value (ParseGateMode maps ""
			// to the default for READ paths, but a PATCH must name a concrete
			// mode). Reject both the empty and the unknown case.
			httpresp.WriteError(w, http.StatusBadRequest, "bundle_gate_mode must be one of: strict, advisory, mandatory_tests")
			return
		}
		gateMode = string(m)
	}

	// Resolve the actor id for the audit row. Prefer the JWT
	// subject (sub claim) when present; fall back to the slice-034
	// credential's UserID.
	actorUserID := actorFromContext(ctx)

	// Single transaction: read current row (for audit before/after); UPDATE
	// the requested field(s); INSERT one me_audit_log row per mutator. All run
	// under the caller's tenant GUC.
	var (
		before tenantWire
		after  tenantWire
	)
	_ = callerTenantID // already validated; tenancy.ApplyTenant reads from ctx
	err = h.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		current, gerr := q.GetTenantByID(ctx, pgtypeUUID(id))
		if gerr != nil {
			if errors.Is(gerr, pgx.ErrNoRows) {
				return errTenantNotFound
			}
			return fmt.Errorf("read tenant: %w", gerr)
		}
		before = tenantFromGetRow(current)
		after = before

		if req.Name != nil {
			updated, uerr := q.UpdateTenantName(ctx, dbx.UpdateTenantNameParams{
				ID:   pgtypeUUID(id),
				Name: normalizedName,
			})
			if uerr != nil {
				// Duplicate name → 409. The expression UNIQUE index
				// `idx_tenants_lower_name` raises sqlstate 23505.
				var pgErr *pgconn.PgError
				if errors.As(uerr, &pgErr) && pgErr.Code == "23505" {
					return errDuplicateName
				}
				return fmt.Errorf("update tenant name: %w", uerr)
			}
			after = tenantFromUpdateNameRow(updated)

			beforeBlob, _ := json.Marshal(map[string]any{"name": before.Name})
			afterBlob, _ := json.Marshal(map[string]any{"name": after.Name})
			if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
				TenantID: pgtypeUUID(id),
				UserID:   pgtypeUUID(actorUserID),
				Action:   auditActionTenantRename,
				Before:   beforeBlob,
				After:    afterBlob,
			}); err != nil {
				return fmt.Errorf("audit insert (rename): %w", err)
			}
		}

		if req.BundleGateMode != nil {
			updated, uerr := q.UpdateTenantBundleGateMode(ctx, dbx.UpdateTenantBundleGateModeParams{
				ID:             pgtypeUUID(id),
				BundleGateMode: gateMode,
			})
			if uerr != nil {
				return fmt.Errorf("update tenant gate mode: %w", uerr)
			}
			beforeMode := after.BundleGateMode // post-rename baseline if both changed
			after = tenantFromUpdateGateModeRow(updated)

			beforeBlob, _ := json.Marshal(map[string]any{"bundle_gate_mode": beforeMode})
			afterBlob, _ := json.Marshal(map[string]any{"bundle_gate_mode": after.BundleGateMode})
			if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
				TenantID: pgtypeUUID(id),
				UserID:   pgtypeUUID(actorUserID),
				Action:   auditActionTenantGatePolicy,
				Before:   beforeBlob,
				After:    afterBlob,
			}); err != nil {
				return fmt.Errorf("audit insert (gate policy): %w", err)
			}
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errDuplicateName):
			httpresp.WriteError(w, http.StatusConflict, "another tenant already uses this name")
		case errors.Is(err, errTenantNotFound):
			// Same 404-shaped body — RLS would also have swallowed
			// the row; the explicit check is for clarity.
			httpresp.WriteError(w, http.StatusNotFound, "tenant not found")
		default:
			httperr.WriteInternal(w, r, "update tenant", err)
		}
		return
	}

	// Slice 126: fan out the meta-audit row(s) to the external sink.
	if req.Name != nil {
		sinkPayload, _ := json.Marshal(map[string]any{
			"before": map[string]any{"name": before.Name},
			"after":  map[string]any{"name": after.Name},
		})
		sink.EmitDefault(ctx, unifiedlog.Entry{
			OccurredAt:    time.Now().UTC(),
			ActorID:       actorUserID.String(),
			TenantID:      id,
			Kind:          unifiedlog.KindMe,
			TargetType:    "tenant",
			TargetID:      id.String(),
			Action:        auditActionTenantRename,
			RowID:         uuid.New(),
			SubjectModule: unifiedlog.SubjectModuleCore,
			PayloadJSON:   sinkPayload,
		})
	}
	if req.BundleGateMode != nil {
		sinkPayload, _ := json.Marshal(map[string]any{
			"before": map[string]any{"bundle_gate_mode": before.BundleGateMode},
			"after":  map[string]any{"bundle_gate_mode": after.BundleGateMode},
		})
		sink.EmitDefault(ctx, unifiedlog.Entry{
			OccurredAt:    time.Now().UTC(),
			ActorID:       actorUserID.String(),
			TenantID:      id,
			Kind:          unifiedlog.KindMe,
			TargetType:    "tenant",
			TargetID:      id.String(),
			Action:        auditActionTenantGatePolicy,
			RowID:         uuid.New(),
			SubjectModule: unifiedlog.SubjectModuleCore,
			PayloadJSON:   sinkPayload,
		})
	}

	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"tenant": after})
}

// callerCanRename returns true when the request was issued by a
// per-tenant admin OR a super_admin. The JWT claim path is the
// preferred check (slice-190 cutover); the cred.IsAdmin fallback
// honors callers still on the slice-034 credential path.
func (h *Handler) callerCanRename(ctx context.Context) bool {
	if claims := jwtmw.FromContext(ctx); claims != nil {
		// SuperAdmin is the global override.
		if claims.SuperAdmin {
			return true
		}
		// Per-tenant admin via the JWT roles map for the caller's
		// CURRENT tenant id.
		if claims.CurrentTenantID != uuid.Nil {
			for _, role := range claims.Roles[claims.CurrentTenantID] {
				if role == "admin" {
					return true
				}
			}
		}
	}
	if cred, ok := authctx.CredentialFromContext(ctx); ok {
		if cred.IsAdmin {
			return true
		}
	}
	return false
}

// inTx opens a transaction under the caller's tenant GUC and invokes
// fn. ApplyTenant reads tenant id from ctx (slice-033 tenancymw) and
// runs `SELECT set_config('app.current_tenant', ...)`; every
// downstream query inherits the RLS scope.
func (h *Handler) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("apply tenant: %w", err)
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ----- helpers -----

var (
	errDuplicateName  = errors.New("tenants: duplicate name")
	errTenantNotFound = errors.New("tenants: not found")
)

// normalizeName trims whitespace, NFC-normalizes, enforces the byte
// cap, and rejects control characters / null bytes. Returns the
// canonical string the DB stores.
func normalizeName(in string) (string, error) {
	trimmed := strings.TrimSpace(in)
	if trimmed == "" {
		return "", errors.New("name must not be empty")
	}
	// NFC normalization — Cyrillic А and Latin A still differ post-NFC
	// (the confusables question is recorded as accepted-risk per the
	// migration header D5), but visually-equivalent decomposed forms
	// collapse here.
	nfc := norm.NFC.String(trimmed)
	if !utf8.ValidString(nfc) {
		return "", errors.New("name must be valid UTF-8")
	}
	if len(nfc) > maxNameLenBytes {
		return "", fmt.Errorf("name exceeds %d UTF-8 bytes", maxNameLenBytes)
	}
	for _, r := range nfc {
		// Reject control chars (Cc) + null. Whitespace inside the
		// string is fine; trim above removed leading/trailing.
		if r == 0 {
			return "", errors.New("name must not contain null bytes")
		}
		if unicode.IsControl(r) {
			return "", errors.New("name must not contain control characters")
		}
	}
	return nfc, nil
}

// actorFromContext resolves the actor id for the audit row. JWT sub
// claim first; falls back to the slice-034 credential's UserID.
// Returns uuid.Nil for non-UUID actor ids (bootstrap-key callers
// etc.) — the audit row still writes with a zero-UUID and the
// payload preserves the actor's stable string via the sink fanout.
func actorFromContext(ctx context.Context) uuid.UUID {
	if claims := jwtmw.FromContext(ctx); claims != nil {
		if u, err := uuid.Parse(claims.Subject); err == nil {
			return u
		}
	}
	if cred, ok := authctx.CredentialFromContext(ctx); ok {
		if u, err := uuid.Parse(cred.UserID); err == nil {
			return u
		}
	}
	return uuid.Nil
}

// tenantFromGetRow / tenantFromUpdateNameRow / tenantFromUpdateGateModeRow
// adapt the three sqlc row shapes (identical fields, distinct generated types)
// to the wire shape. Slice 608 added bundle_gate_mode to all three.
func tenantFromGetRow(r dbx.GetTenantByIDRow) tenantWire {
	return tenantWire{
		ID:                uuid.UUID(r.ID.Bytes).String(),
		Name:              r.Name,
		IsBootstrapTenant: r.IsBootstrapTenant,
		BundleGateMode:    r.BundleGateMode,
		CreatedAt:         r.CreatedAt.Time,
		UpdatedAt:         r.UpdatedAt.Time,
	}
}

func tenantFromUpdateNameRow(r dbx.UpdateTenantNameRow) tenantWire {
	return tenantWire{
		ID:                uuid.UUID(r.ID.Bytes).String(),
		Name:              r.Name,
		IsBootstrapTenant: r.IsBootstrapTenant,
		BundleGateMode:    r.BundleGateMode,
		CreatedAt:         r.CreatedAt.Time,
		UpdatedAt:         r.UpdatedAt.Time,
	}
}

func tenantFromUpdateGateModeRow(r dbx.UpdateTenantBundleGateModeRow) tenantWire {
	return tenantWire{
		ID:                uuid.UUID(r.ID.Bytes).String(),
		Name:              r.Name,
		IsBootstrapTenant: r.IsBootstrapTenant,
		BundleGateMode:    r.BundleGateMode,
		CreatedAt:         r.CreatedAt.Time,
		UpdatedAt:         r.UpdatedAt.Time,
	}
}

func pgtypeUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
