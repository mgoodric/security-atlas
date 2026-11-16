// Vendor owners are users (#2129).
//
// owner_user_id names the owner, an active user of the vendor's own tenant.
// The free-text owner_user it replaces is kept: while owner_user_id is NULL,
// its text is the display-only "Owner (unlinked)". Picking an owner clears
// the text. Every change to either writes one me_audit_log row,
// 'vendor_owner_update', in the write's transaction, naming the vendor and
// the before and after owner.

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

// auditActionOwnerUpdate is the me_audit_log action for an owner change.
// The CHECK constraint (20261218000000_risk_vendor_owner_users.sql) lists it.
const auditActionOwnerUpdate = "vendor_owner_update"

// ownerState is one side of a vendor_owner_update row.
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

// auditOwnerChange writes the vendor_owner_update row when before and after
// differ, and nothing when they are the same.
func auditOwnerChange(ctx context.Context, q *dbx.Queries, tenantID, actor uuid.UUID, before, after ownerState) error {
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
	if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
		TenantID: pgUUID(tenantID),
		UserID:   pgUUID(actor),
		Action:   auditActionOwnerUpdate,
		Before:   beforeBlob,
		After:    afterBlob,
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
