// Vendor owners are users (#2129).
//
// owner_user_id names the owner, an active user of the vendor's own tenant.
// The free-text owner_user it replaces is kept: while owner_user_id is NULL,
// its text is the display-only "Owner (unlinked)". Picking an owner clears
// the text. Every change to either writes one vendor_audit_log row,
// 'owner_changed', in the write's transaction, naming the vendor, the
// caller's principal and the before and after owner (#2450). Rows #2129
// wrote to me_audit_log ('vendor_owner_update') stay there.

package vendor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrOwnerNotActiveUser is returned when an owner_user_id does not name an
// active user of the vendor's tenant. The HTTP layer renders it as a 422.
var ErrOwnerNotActiveUser = errors.New("vendor: owner is not an active user in this tenant")

// auditActionOwnerChanged is the vendor_audit_log action for an owner
// change. The CHECK constraint (20261260000000_vendor_audit_log.sql) lists
// it.
const auditActionOwnerChanged = "owner_changed"

// systemActor names a write that has no caller (the demo seeder, a test):
// vendor_audit_log.actor is never empty.
const systemActor = "system:vendor-store"

// ownerState is one side of an owner_changed row.
type ownerState struct {
	VendorID      string  `json:"vendor_id"`
	OwnerUserID   *string `json:"owner_user_id"`
	OwnerUnlinked string  `json:"owner_unlinked"`
}

func ownerStateOf(vendorID uuid.UUID, owner *uuid.UUID, text string) ownerState {
	s := ownerState{VendorID: vendorID.String(), OwnerUnlinked: strings.TrimSpace(text)}
	if owner != nil {
		id := owner.String()
		s.OwnerUserID = &id
		s.OwnerUnlinked = ""
	}
	return s
}

func (a ownerState) equal(b ownerState) bool {
	sameID := (a.OwnerUserID == nil && b.OwnerUserID == nil) ||
		(a.OwnerUserID != nil && b.OwnerUserID != nil && *a.OwnerUserID == *b.OwnerUserID)
	return sameID && a.OwnerUnlinked == b.OwnerUnlinked
}

// normalizeOwner applies the rule that a picked owner replaces the free
// text: with an owner set, the text is not stored.
func normalizeOwner(in *CreateVendorInput) {
	in.OwnerUser = strings.TrimSpace(in.OwnerUser)
	if in.OwnerUserID != nil {
		in.OwnerUser = ""
	}
}

// checkOwnerUser refuses an owner that is not an active user of tenantID.
// A nil owner needs no check.
func checkOwnerUser(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, owner *uuid.UUID) error {
	if owner == nil {
		return nil
	}
	ok, err := q.UserExistsInTenant(ctx, dbx.UserExistsInTenantParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(*owner),
	})
	if err != nil {
		return fmt.Errorf("check owner: %w", err)
	}
	if !ok {
		return ErrOwnerNotActiveUser
	}
	return nil
}

// auditOwnerChange writes the owner_changed row when before and after
// differ, and nothing when they are the same. actor is the caller's
// principal; empty means no caller.
func auditOwnerChange(ctx context.Context, q *dbx.Queries, tenantID, vendorID uuid.UUID, actor string, before, after ownerState) error {
	if before.equal(after) {
		return nil
	}
	beforeBlob, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("vendor: audit before: %w", err)
	}
	afterBlob, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("vendor: audit after: %w", err)
	}
	if actor == "" {
		actor = systemActor
	}
	if err := q.InsertVendorAuditLog(ctx, dbx.InsertVendorAuditLogParams{
		TenantID:    pgUUID(tenantID),
		VendorID:    pgUUID(vendorID),
		Actor:       actor,
		ActionType:  auditActionOwnerChanged,
		BeforeState: beforeBlob,
		AfterState:  afterBlob,
	}); err != nil {
		return fmt.Errorf("vendor: audit owner change: %w", err)
	}
	return nil
}

func pgUUIDPtr(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgUUID(*u)
}

func uuidPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}
