package riskimport

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Owners arrive as linked people where possible and are never lost where
// not (spec #2812, Q15; #2843):
//
//   - an owner cell is matched by email, ignoring case, to an active user
//     of the uploading tenant, and that user becomes the risk's linked
//     owner (risks.owner_user_id);
//   - a cell that matches no such user (a name, an unknown or disabled
//     address, another tenant's user) is kept as the risk's unlinked
//     free-text owner, and the row is flagged owner_unlinked so the
//     approver knows to invite the person.
//
// Matching happens at staging; approval re-checks the user is still active
// and, when not, creates the risk with their display name as the unlinked
// owner.
//
// A staged row carries only the matched user's id, never their email or
// name: the review page resolves the id through /v1/people, under the
// caller's own access. That a row is linked or unlinked still says whether
// an address belongs to an active user of the uploader's own tenant; that
// is accepted (docs/audit-log/2843-risk-import-owner-decisions.md D4).

// FieldOwner is the owner column. Like the others it is a column-map key.
const FieldOwner = "owner"

// resolveOwners maps each owner cell, lowercased, to the active user of
// the caller's tenant whose email it is. A cell that names no such user is
// absent from the result. Another tenant's users are invisible under RLS
// and excluded by the tenant predicate besides; an address held by two
// users of the tenant (users_email_per_tenant_unique is case-sensitive)
// matches neither.
func (s *Store) resolveOwners(ctx context.Context, owners []string) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	var emails []string
	seen := map[string]bool{}
	for _, o := range owners {
		key := ownerKey(o)
		if key == "" || seen[key] || !strings.Contains(key, "@") {
			continue
		}
		seen[key] = true
		emails = append(emails, key)
	}
	if len(emails) == 0 {
		return out, nil
	}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT lower(email), (array_agg(id))[1]
			FROM users
			WHERE tenant_id = $1 AND status = 'active' AND lower(email) = ANY($2::text[])
			GROUP BY lower(email)
			HAVING count(*) = 1
		`, tenantID, emails)
		if err != nil {
			return fmt.Errorf("riskimport: resolve owners: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var id uuid.UUID
			if err := rows.Scan(&key, &id); err != nil {
				return fmt.Errorf("riskimport: scan owner: %w", err)
			}
			out[key] = id
		}
		return rows.Err()
	})
	return out, err
}

// applyOwner sets the row's owner from its cell: the linked user when the
// cell resolved, else the cell as the unlinked owner with the
// owner_unlinked flag. A blank cell leaves the row without an owner.
func applyOwner(row *BatchRow, owner string, resolved map[string]uuid.UUID) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return
	}
	if id, ok := resolved[ownerKey(owner)]; ok {
		row.OwnerUserID = id.String()
		return
	}
	row.OwnerUnlinked = owner
	row.Flags = append(row.Flags, FlagOwnerUnlinked)
}

func ownerKey(owner string) string { return strings.ToLower(strings.TrimSpace(owner)) }

// approvalOwner is the owner a staged row's risk is created with: the
// linked user while still active in the tenant, else that user's display
// name (their email when they have none) as the unlinked owner: a user
// disabled since upload is no longer an owner a risk can link to. A row
// staged unlinked keeps its text.
func approvalOwner(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, id *uuid.UUID, unlinked string) (*uuid.UUID, string, error) {
	if id == nil {
		return nil, unlinked, nil
	}
	var active bool
	var name string
	err := tx.QueryRow(ctx, `
		SELECT status = 'active', COALESCE(NULLIF(btrim(display_name), ''), email)
		FROM users WHERE tenant_id = $1 AND id = $2
	`, tenantID, *id).Scan(&active, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, unlinked, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("riskimport: check owner: %w", err)
	}
	if !active {
		return nil, name, nil
	}
	return id, "", nil
}
