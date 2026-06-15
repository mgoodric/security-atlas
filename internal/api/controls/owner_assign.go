// Slice 468 — server-backed control owner-assignment (single-item + bulk).
//
// This file is the backend completion of slice 448's frontend shell. It
// invents the per-control owner-USER assignment path (which did not exist
// on `main`: controls carried only a read-only `owner_role` RACI string),
// then builds the bulk path AS A LOOP over the single-item per-item check.
//
// THE LOAD-BEARING DESIGN (AC-11 / P0-467-1 — the entire reason this slice
// exists): the bulk path is NEVER weaker than the single-item path and
// NEVER re-implements its authz. Both call the SAME assignOwnerInTx helper
// per control. There is exactly one place where "may this owner be assigned
// to this control in this tenant?" is decided, so the two paths cannot
// drift.
//
// Authorization is layered:
//  1. ROLE gate — the shared authz middleware (internal/api/authzmw) gates
//     BOTH routes identically: POST resolves to action="write",
//     resource="controls", admitted only for admin / grc_engineer /
//     control_owner (denied for viewer / auditor). The ":bulk-assign-owner"
//     suffix is deliberately NOT a transition verb, so the bulk route gets
//     the same write-on-controls decision the single route does.
//  2. PER-ITEM tenant + existence check — assignOwnerInTx reads each
//     control through the tenant-GUC tx; RLS hides cross-tenant rows, so a
//     control id belonging to another tenant is rejected (never silently
//     applied — P0-448-2). This is the amplifier defense the middleware
//     cannot provide (it sees the request once; the per-control set is the
//     handler's responsibility).
//  3. TARGET-OWNER validation — the owner_user_id must be a real, active
//     user in the calling tenant (threat-model T); a cross-tenant or
//     disabled owner is rejected.
//
// No silent partial apply (P0-448-2): the bulk path runs every item in ONE
// transaction; if ANY item fails its per-item check the whole transaction
// rolls back and the caller gets a 4xx naming the first offending id. The
// caller never ends up with "some applied, some not" with no signal.
//
// Audit (threat-model R / P0-448-4): every assign — single or bulk — writes
// one append-only row to control_owner_assignment_audit_log capturing the
// actor, the target owner, and the SET of affected control ids. The bulk
// event is ONE row referencing N ids.
//
// Cap (threat-model D / P0-448-3): the bulk request is capped at
// BulkAssignCap ids per request; over-cap is rejected (the client chunks).
package controls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// BulkAssignCap is the server-side per-request ceiling on the bulk-assign
// set. It mirrors the client SELECTION_CAP (web .../selection.ts = 200) but
// is the REAL security boundary: the client cap is ergonomics, this is the
// bounded-transaction guard (threat-model D). A request over the cap is
// rejected with 422 so the caller chunks rather than the server silently
// truncating.
const BulkAssignCap = 200

// assignErrKind classifies a per-item assignment failure so the single and
// bulk handlers map it to the same HTTP status with the same wording.
type assignErrKind int

const (
	assignErrNone assignErrKind = iota
	// assignErrControlNotFound: the control id does not exist in the
	// caller's tenant (RLS hid it, or it is superseded). 404 single; the
	// bulk path fails the batch naming this id.
	assignErrControlNotFound
	// assignErrOwnerNotInTenant: the target owner_user_id is not a real,
	// active user in the caller's tenant. 422.
	assignErrOwnerNotInTenant
)

// assignItemError carries the per-item failure kind + the offending control
// id (for the bulk "which one failed" message). It is the single shape both
// paths surface.
type assignItemError struct {
	kind      assignErrKind
	controlID uuid.UUID
}

func (e assignItemError) Error() string {
	switch e.kind {
	case assignErrControlNotFound:
		return fmt.Sprintf("control %s not found in tenant", e.controlID)
	case assignErrOwnerNotInTenant:
		return "target owner is not an active user in this tenant"
	default:
		return "assignment error"
	}
}

// OwnerAssignHandler binds the single-item + bulk owner-assign routes. pool
// may be nil in unit-only servers that exercise the pre-DB branches
// (401/400); the read/write path gates return 503 in that case.
type OwnerAssignHandler struct {
	pool *pgxpool.Pool
}

// NewOwnerAssignHandler constructs the handler. pool is the platform pgx
// pool (nil-safe for unit servers).
func NewOwnerAssignHandler(pool *pgxpool.Pool) *OwnerAssignHandler {
	return &OwnerAssignHandler{pool: pool}
}

// ----- wire types -----

type assignOwnerRequest struct {
	// OwnerUserID is the target owner — a users.id UUID in the caller's
	// tenant. Required.
	OwnerUserID string `json:"owner_user_id"`
}

type assignOwnerResponse struct {
	ControlID   string `json:"control_id"`
	OwnerUserID string `json:"owner_user_id"`
	AssignedBy  string `json:"assigned_by"`
}

type bulkAssignOwnerRequest struct {
	// ControlIDs is the set to assign — capped at BulkAssignCap. Required,
	// non-empty.
	ControlIDs []string `json:"control_ids"`
	// OwnerUserID is the single target owner applied to every control in
	// the set. Required.
	OwnerUserID string `json:"owner_user_id"`
}

type bulkAssignOwnerResponse struct {
	OwnerUserID string `json:"owner_user_id"`
	AssignedBy  string `json:"assigned_by"`
	// Assigned is the count of controls the owner was assigned to (== the
	// submitted set size; the operation is all-or-nothing, so a 200 means
	// every id was applied).
	Assigned   int      `json:"assigned"`
	ControlIDs []string `json:"control_ids"`
}

type assignErrorBody struct {
	Error string `json:"error"`
}

// ----- single-item handler -----

// AssignOwner serves POST /v1/controls/{id}/owner. The prerequisite
// single-item path the bulk path reuses (AC-467-1). The middleware has
// already gated the caller's role (write-on-controls); this handler does the
// per-item tenant/existence + target-owner validation and the write, then
// audits the single id.
func (h *OwnerAssignHandler) AssignOwner(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeAssignError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeAssignError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}
	req, ok := decodeAssignBody(w, r)
	if !ok {
		return
	}
	ownerUUID, ok := parseOwner(w, req.OwnerUserID)
	if !ok {
		return
	}
	if h.pool == nil {
		writeAssignError(w, http.StatusServiceUnavailable, "control store not configured")
		return
	}
	tenantUUID, actorUUID, ok := h.identity(w, cred)
	if !ok {
		return
	}

	var applied dbx.ControlOwnerAssignment
	txErr := h.inTenantTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		row, aerr := assignOwnerInTx(ctx, q, tenantUUID, actorUUID, controlID, ownerUUID)
		if aerr != nil {
			return aerr
		}
		applied = row
		// Audit: one row, is_bulk=false, control_ids = the single id.
		_, ierr := q.InsertOwnerAssignmentAudit(ctx, dbx.InsertOwnerAssignmentAuditParams{
			TenantID:    pgUUID(tenantUUID),
			ActorUserID: pgUUID(actorUUID),
			OwnerUserID: pgUUID(ownerUUID),
			ControlIds:  []pgtype.UUID{pgUUID(controlID)},
			IsBulk:      false,
		})
		return ierr
	})
	if txErr != nil {
		h.writeAssignTxError(w, txErr)
		return
	}

	writeAssignJSON(w, http.StatusOK, assignOwnerResponse{
		ControlID:   uuid.UUID(applied.ControlID.Bytes).String(),
		OwnerUserID: uuid.UUID(applied.OwnerUserID.Bytes).String(),
		AssignedBy:  uuid.UUID(applied.AssignedBy.Bytes).String(),
	})
}

// ----- bulk handler -----

// BulkAssignOwner serves POST /v1/controls:bulk-assign-owner. The
// authz-amplifier. It validates the cap + the target owner ONCE, then loops
// assignOwnerInTx over the set INSIDE A SINGLE TRANSACTION — the per-item
// check is byte-identical to the single-item path (it is the same function).
// ANY per-item failure rolls the whole transaction back (no silent partial
// apply); the caller gets a 4xx naming the offending id. One bulk audit row
// references the whole applied set.
func (h *OwnerAssignHandler) BulkAssignOwner(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeAssignError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	body, ok := decodeBulkBody(w, r)
	if !ok {
		return
	}
	if len(body.ControlIDs) == 0 {
		writeAssignError(w, http.StatusBadRequest, "control_ids must be a non-empty array")
		return
	}
	// Cap check BEFORE any work (threat-model D / P0-448-3). Over-cap is
	// 422 so the client chunks; the server never truncates silently.
	if len(body.ControlIDs) > BulkAssignCap {
		writeAssignError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("control_ids exceeds the %d-per-request cap; chunk the request", BulkAssignCap))
		return
	}
	ownerUUID, ok := parseOwner(w, body.OwnerUserID)
	if !ok {
		return
	}
	// Parse + de-dupe the ids up front so a malformed id fails fast and the
	// audit set is exact. Preserve first-seen order for a deterministic
	// audit array + response.
	controlUUIDs, dupFree, perr := parseControlIDs(body.ControlIDs)
	if perr != nil {
		writeAssignError(w, http.StatusBadRequest, perr.Error())
		return
	}
	if h.pool == nil {
		writeAssignError(w, http.StatusServiceUnavailable, "control store not configured")
		return
	}
	tenantUUID, actorUUID, ok := h.identity(w, cred)
	if !ok {
		return
	}

	txErr := h.inTenantTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		for _, cid := range controlUUIDs {
			// PER-ITEM: the SAME check the single-item path runs. A
			// cross-tenant / missing control or a bad owner fails the
			// whole transaction here (rollback) — no silent partial apply.
			if _, aerr := assignOwnerInTx(ctx, q, tenantUUID, actorUUID, cid, ownerUUID); aerr != nil {
				return aerr
			}
		}
		// One bulk audit row referencing the whole applied set.
		ctrlPg := make([]pgtype.UUID, len(controlUUIDs))
		for i, cid := range controlUUIDs {
			ctrlPg[i] = pgUUID(cid)
		}
		_, ierr := q.InsertOwnerAssignmentAudit(ctx, dbx.InsertOwnerAssignmentAuditParams{
			TenantID:    pgUUID(tenantUUID),
			ActorUserID: pgUUID(actorUUID),
			OwnerUserID: pgUUID(ownerUUID),
			ControlIds:  ctrlPg,
			IsBulk:      true,
		})
		return ierr
	})
	if txErr != nil {
		h.writeAssignTxError(w, txErr)
		return
	}

	writeAssignJSON(w, http.StatusOK, bulkAssignOwnerResponse{
		OwnerUserID: ownerUUID.String(),
		AssignedBy:  actorUUID.String(),
		Assigned:    len(controlUUIDs),
		ControlIDs:  dupFree,
	})
}

// ----- shared per-item logic (the single source of truth) -----

// assignOwnerInTx is the ONE place an owner-assignment is authorized + applied
// per control. The single-item handler calls it once; the bulk handler calls
// it per id in a loop. Both run it inside a tenant-GUC tx so RLS is active.
//
// Steps (in order — fail fast):
//  1. control exists + visible to tenant (RLS) — else assignErrControlNotFound
//  2. target owner is an active tenant user — else assignErrOwnerNotInTenant
//  3. UPSERT the (tenant, control) -> owner row
//
// It does NOT write the audit row — the caller owns the audit shape (one row
// per single assign; one bulk row per bulk assign) so the audit is exactly
// the spec's "one event referencing N items" for bulk.
func assignOwnerInTx(
	ctx context.Context,
	q *dbx.Queries,
	tenantID, actorID, controlID, ownerID uuid.UUID,
) (dbx.ControlOwnerAssignment, error) {
	var zero dbx.ControlOwnerAssignment

	// 1. Per-item tenant + existence check. RLS hides cross-tenant rows, so
	//    a control in another tenant returns false here — the amplifier
	//    defense. (AC-7 / threat-model E.)
	exists, err := q.ControlExistsInTenant(ctx, dbx.ControlExistsInTenantParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(controlID),
	})
	if err != nil {
		return zero, err
	}
	if !exists {
		return zero, assignItemError{kind: assignErrControlNotFound, controlID: controlID}
	}

	// 2. Target-owner validation (threat-model T). RLS hides cross-tenant
	//    users; the status='active' gate rejects a disabled owner.
	ownerOK, err := q.UserExistsInTenant(ctx, dbx.UserExistsInTenantParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(ownerID),
	})
	if err != nil {
		return zero, err
	}
	if !ownerOK {
		return zero, assignItemError{kind: assignErrOwnerNotInTenant, controlID: controlID}
	}

	// 3. Write. The (tenant_id) WITH CHECK on the RLS write policy is a
	//    second tenant guard at the DB layer (invariant #6).
	return q.UpsertControlOwner(ctx, dbx.UpsertControlOwnerParams{
		TenantID:    pgUUID(tenantID),
		ControlID:   pgUUID(controlID),
		OwnerUserID: pgUUID(ownerID),
		AssignedBy:  pgUUID(actorID),
	})
}

// ----- helpers -----

// inTenantTx runs fn inside a read-write tx with the tenant GUC applied so
// RLS is enforced exactly as production. Commits on nil error, rolls back
// otherwise — the all-or-nothing guarantee the bulk path relies on.
func (h *OwnerAssignHandler) inTenantTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if terr := tenancy.ApplyTenant(ctx, tx); terr != nil {
		return terr
	}
	if ferr := fn(ctx, dbx.New(tx)); ferr != nil {
		return ferr
	}
	return tx.Commit(ctx)
}

// identity resolves the tenant + actor user UUIDs from the credential. The
// actor id is the verified credential's user (NOT the request body) — a
// caller cannot forge who performed the assignment (threat-model R). The
// credential's UserID carries the auth-substrate "user:" prefix, stripped
// via jwtmw.SubjectUserID before parse (the adminusers / export precedent).
func (h *OwnerAssignHandler) identity(w http.ResponseWriter, cred credstore.Credential) (uuid.UUID, uuid.UUID, bool) {
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeAssignError(w, http.StatusInternalServerError, "tenant context: invalid tenant id")
		return uuid.Nil, uuid.Nil, false
	}
	actorUUID, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		// A machine/api-key credential has no users.id-shaped UserID;
		// owner-assignment is a human action (the actor is recorded for
		// repudiation), so reject rather than tag uuid.Nil.
		writeAssignError(w, http.StatusForbidden, "owner assignment requires a user credential")
		return uuid.Nil, uuid.Nil, false
	}
	return tenantUUID, actorUUID, true
}

func writeAssignJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAssignError(w http.ResponseWriter, status int, msg string) {
	writeAssignJSON(w, status, assignErrorBody{Error: msg})
}

// writeAssignTxError maps a transaction error to the right status. A typed
// assignItemError surfaces as 404 (missing control) or 422 (bad owner);
// anything else is a 500.
func (h *OwnerAssignHandler) writeAssignTxError(w http.ResponseWriter, err error) {
	var item assignItemError
	if errors.As(err, &item) {
		switch item.kind {
		case assignErrControlNotFound:
			writeAssignError(w, http.StatusNotFound, item.Error())
			return
		case assignErrOwnerNotInTenant:
			writeAssignError(w, http.StatusUnprocessableEntity, item.Error())
			return
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeAssignError(w, http.StatusNotFound, "control not found")
		return
	}
	writeAssignError(w, http.StatusInternalServerError, "assign owner: "+err.Error())
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// decodeAssignBody reads + validates the single-item request body.
func decodeAssignBody(w http.ResponseWriter, r *http.Request) (assignOwnerRequest, bool) {
	var req assignOwnerRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAssignError(w, http.StatusBadRequest, "read body: "+err.Error())
		return req, false
	}
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		writeAssignError(w, http.StatusBadRequest, "invalid JSON body: "+jerr.Error())
		return req, false
	}
	return req, true
}

// decodeBulkBody reads + validates the bulk request body envelope.
func decodeBulkBody(w http.ResponseWriter, r *http.Request) (bulkAssignOwnerRequest, bool) {
	var req bulkAssignOwnerRequest
	// 200 ids * ~40 bytes + envelope — 64 KiB is generous and bounds a
	// runaway body.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAssignError(w, http.StatusBadRequest, "read body: "+err.Error())
		return req, false
	}
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		writeAssignError(w, http.StatusBadRequest, "invalid JSON body: "+jerr.Error())
		return req, false
	}
	return req, true
}

// parseOwner parses + validates the target owner id.
func parseOwner(w http.ResponseWriter, raw string) (uuid.UUID, bool) {
	if raw == "" {
		writeAssignError(w, http.StatusBadRequest, "owner_user_id is required")
		return uuid.Nil, false
	}
	u, err := uuid.Parse(raw)
	if err != nil {
		writeAssignError(w, http.StatusBadRequest, "owner_user_id must be a uuid")
		return uuid.Nil, false
	}
	return u, true
}

// parseControlIDs parses the bulk id set, rejecting any malformed id, and
// returns both the parsed UUIDs (de-duped, first-seen order) and the
// de-duped string form for the response echo.
func parseControlIDs(raw []string) ([]uuid.UUID, []string, error) {
	seen := make(map[uuid.UUID]struct{}, len(raw))
	out := make([]uuid.UUID, 0, len(raw))
	strs := make([]string, 0, len(raw))
	for _, s := range raw {
		u, err := uuid.Parse(s)
		if err != nil {
			return nil, nil, fmt.Errorf("control_ids contains a non-uuid value: %q", s)
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
		strs = append(strs, u.String())
	}
	return out, strs, nil
}
