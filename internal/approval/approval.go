// Package approval holds the one approval rule every record lifecycle
// shares (#2101, ADR-0024): only a person approves, and never the person
// who requested the approval, unless the organisation has turned on
// "Allow self-approval". An approval that relies on that setting is
// reported as self-approved so the caller can flag the record.
//
// Policies (#1807), framework scopes and exceptions call Check; risk
// acceptance adopts it when its transitions are built. Each module maps
// the errors below onto its own error values and HTTP statuses.
package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

var (
	// ErrNotPerson is returned when the approving credential belongs to no
	// user: an unbound API key, an OAuth client or the bootstrap admin.
	// Only people approve, with or without self-approval (#1807).
	ErrNotPerson = errors.New("approval: only a person can approve")
	// ErrSelfApproval is returned when the approver is the requester and
	// the organisation does not allow self-approval.
	ErrSelfApproval = errors.New("approval: the requester cannot approve their own request")
	// ErrRequesterUnknown is returned when the requester was recorded as a
	// session id ("jwt:<token id>") rather than a person, so no approver
	// can be shown to differ from them.
	ErrRequesterUnknown = errors.New("approval: the requester was recorded by session, not by person")
)

// legacySessionPrefix marks a requester recorded from a JWT credential id
// before requesters were recorded by person (#1807). It names a session.
const legacySessionPrefix = "jwt:"

// Approver names who is approving. UserID is the person, the users(id)
// uuid behind the credential (credstore.CallerUserID), empty when the
// credential belongs to no user. CredentialID is the credential itself,
// compared against requesters recorded by credential id.
type Approver struct {
	UserID       string
	CredentialID string
}

// Check applies the rule. requester is the recorded requester (a users(id)
// uuid, or a credential id for rows written before #1807 or by a machine);
// empty means none was recorded and there is nothing to compare.
// allowSelf is the organisation's Allow self-approval setting.
//
// selfApproved is true only when the approval succeeds because allowSelf
// is on and the approver is the requester.
func Check(requester string, a Approver, allowSelf bool) (selfApproved bool, err error) {
	if a.UserID == "" {
		return false, ErrNotPerson
	}
	if requester == "" {
		return false, nil
	}
	if strings.HasPrefix(requester, legacySessionPrefix) {
		return false, ErrRequesterUnknown
	}
	if requester != a.UserID && requester != a.CredentialID {
		return false, nil
	}
	if !allowSelf {
		return false, ErrSelfApproval
	}
	return true, nil
}

// AllowSelf reads the tenant's Allow self-approval setting inside the
// caller's transaction, so the setting and the approval it governs are
// read and written together. A tenant with no tenants row (a bare-UUID
// tenant predating slice 144) gets the default, off.
func AllowSelf(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (bool, error) {
	allow, err := q.GetTenantAllowSelfApproval(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("approval: read allow_self_approval: %w", err)
	}
	return allow, nil
}
