package scim

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrGroupNotFound is returned when no group matches under the tenant. A group
// in another tenant reads identically (RLS-confined; no cross-tenant oracle —
// P0-733-4).
var ErrGroupNotFound = errors.New("scim: group not found")

// GroupStore is the DB-backed SCIM Group store (slice 733). Every method runs
// under app.current_tenant RLS (invariant #6 / P0-733-4) — the tenant comes
// from the authenticated SCIM credential, never from the request body.
//
// The store records membership ONLY; it never assigns a role. A membership
// mutation returns the set of AFFECTED user ids so the handler can drive a
// re-derivation through the slice-509 grouprole.Resolver (the sole path to a
// role — P0-733-1 / P0-733-3). The store itself has no resolver dependency,
// keeping it a leaf the role-derivation logic cannot leak into.
type GroupStore struct {
	pool *pgxpool.Pool
}

// NewGroupStore constructs a GroupStore bound to the RLS app pool.
func NewGroupStore(pool *pgxpool.Pool) *GroupStore { return &GroupStore{pool: pool} }

// GroupResult bundles a group's domain projection + its current member ids +
// the users affected by the operation (added or removed members) so the handler
// can re-derive their roles (AC-3).
type GroupResult struct {
	Group         DomainGroup
	MemberIDs     []string
	AffectedUsers []string
}

// CreateGroupInput is the attribute allow-list for a SCIM Group Create. NO role
// field, by design (P0-733-3).
type CreateGroupInput struct {
	DisplayName string
	ExternalID  string
	MemberIDs   []string
}

// CreateGroup creates a SCIM Group + its initial membership (AC-2 Create).
// Reconciles on externalId: a duplicate externalId in the tenant is ErrConflict
// (the IdP should GET/PATCH instead). Every initial member is an affected user.
func (s *GroupStore) CreateGroup(ctx context.Context, tenantID string, in CreateGroupInput) (GroupResult, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return GroupResult{}, err
	}
	memberIDs := dedupeStrings(in.MemberIDs)
	var out GroupResult
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if in.ExternalID != "" {
			if _, gErr := q.GetSCIMGroupByExternalID(ctx, dbx.GetSCIMGroupByExternalIDParams{
				TenantID: tID, ScimExternalID: strPtr(in.ExternalID),
			}); gErr == nil {
				return ErrConflict
			} else if !errors.Is(gErr, pgx.ErrNoRows) {
				return gErr
			}
		}
		row, cErr := q.CreateSCIMGroup(ctx, dbx.CreateSCIMGroupParams{
			ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:       tID,
			DisplayName:    in.DisplayName,
			ScimExternalID: strPtrOrNil(in.ExternalID),
		})
		if cErr != nil {
			if isUniqueViolation(cErr) {
				return ErrConflict
			}
			return fmt.Errorf("scim: create group: %w", cErr)
		}
		g := domainGroupFromRow(row)
		groupRef := g.GroupRef()
		for _, uid := range memberIDs {
			if aErr := q.AddSCIMGroupMember(ctx, dbx.AddSCIMGroupMemberParams{
				ID:       pgtype.UUID{Bytes: uuid.New(), Valid: true},
				TenantID: tID,
				GroupID:  row.ID,
				UserID:   uid,
				GroupRef: groupRef,
			}); aErr != nil {
				return fmt.Errorf("scim: add group member: %w", aErr)
			}
		}
		out = GroupResult{Group: g, MemberIDs: memberIDs, AffectedUsers: memberIDs}
		return nil
	})
	if err != nil {
		return GroupResult{}, err
	}
	return out, nil
}

// GetGroup returns a group + its member ids (AC-2 Get).
func (s *GroupStore) GetGroup(ctx context.Context, tenantID string, id uuid.UUID) (GroupResult, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return GroupResult{}, err
	}
	var out GroupResult
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetSCIMGroupByID(ctx, dbx.GetSCIMGroupByIDParams{
			TenantID: tID, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrGroupNotFound
			}
			return gErr
		}
		members, mErr := q.ListSCIMGroupMembers(ctx, dbx.ListSCIMGroupMembersParams{
			TenantID: tID, GroupID: row.ID,
		})
		if mErr != nil {
			return mErr
		}
		out = GroupResult{Group: domainGroupFromRow(row), MemberIDs: members}
		return nil
	})
	if err != nil {
		return GroupResult{}, err
	}
	return out, nil
}

// ListGroups returns a page of tenant groups (AC-2 List). RLS confines to the
// credential's tenant (P0-733-4). Member ids are NOT loaded per-row (List is a
// summary surface); the wire Group renders an empty members array on List, per
// the common IdP expectation.
func (s *GroupStore) ListGroups(ctx context.Context, tenantID string, limit, offset int) ([]DomainGroup, int, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return nil, 0, err
	}
	var groups []DomainGroup
	var total int
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, lErr := q.ListSCIMGroups(ctx, dbx.ListSCIMGroupsParams{
			TenantID: tID, Limit: clampInt32(limit), Offset: clampInt32(offset),
		})
		if lErr != nil {
			return lErr
		}
		for _, r := range rows {
			groups = append(groups, domainGroupFromRow(r))
		}
		cnt, cErr := q.CountSCIMGroups(ctx, tID)
		if cErr != nil {
			return cErr
		}
		total = int(cnt)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

// FindGroupsByDisplayName returns groups matching `filter=displayName eq "x"`.
func (s *GroupStore) FindGroupsByDisplayName(ctx context.Context, tenantID, displayName string) ([]DomainGroup, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return nil, err
	}
	var groups []DomainGroup
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, lErr := q.ListSCIMGroupsByDisplayName(ctx, dbx.ListSCIMGroupsByDisplayNameParams{
			TenantID: tID, Lower: displayName,
		})
		if lErr != nil {
			return lErr
		}
		for _, r := range rows {
			groups = append(groups, domainGroupFromRow(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return groups, nil
}

// ReplaceGroupInput is the SCIM Group Replace (PUT) allow-list. The members
// array REPLACES the group's membership wholesale (RFC 7644 PUT semantics).
type ReplaceGroupInput struct {
	DisplayName string
	MemberIDs   []string
}

// ReplaceGroup overwrites displayName + the FULL membership set (AC-2 Replace).
// Affected users = the symmetric difference (added ∪ removed) so a re-derivation
// runs for everyone whose membership changed.
func (s *GroupStore) ReplaceGroup(ctx context.Context, tenantID string, id uuid.UUID, in ReplaceGroupInput) (GroupResult, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return GroupResult{}, err
	}
	want := dedupeStrings(in.MemberIDs)
	var out GroupResult
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetSCIMGroupByID(ctx, dbx.GetSCIMGroupByIDParams{
			TenantID: tID, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrGroupNotFound
			}
			return gErr
		}
		prevMembers, mErr := q.ListSCIMGroupMembers(ctx, dbx.ListSCIMGroupMembersParams{
			TenantID: tID, GroupID: row.ID,
		})
		if mErr != nil {
			return mErr
		}
		updated, rErr := q.ReplaceSCIMGroupDisplayName(ctx, dbx.ReplaceSCIMGroupDisplayNameParams{
			TenantID: tID, ID: row.ID, DisplayName: in.DisplayName,
		})
		if rErr != nil {
			return fmt.Errorf("scim: replace group: %w", rErr)
		}
		g := domainGroupFromRow(updated)
		// Reset membership: clear all, then add the wanted set.
		if _, dErr := q.RemoveAllSCIMGroupMembers(ctx, dbx.RemoveAllSCIMGroupMembersParams{
			TenantID: tID, GroupID: row.ID,
		}); dErr != nil {
			return fmt.Errorf("scim: clear group members: %w", dErr)
		}
		groupRef := g.GroupRef()
		for _, uid := range want {
			if aErr := q.AddSCIMGroupMember(ctx, dbx.AddSCIMGroupMemberParams{
				ID:       pgtype.UUID{Bytes: uuid.New(), Valid: true},
				TenantID: tID, GroupID: row.ID, UserID: uid, GroupRef: groupRef,
			}); aErr != nil {
				return fmt.Errorf("scim: add group member: %w", aErr)
			}
		}
		out = GroupResult{
			Group:         g,
			MemberIDs:     want,
			AffectedUsers: symmetricDiff(prevMembers, want),
		}
		return nil
	})
	if err != nil {
		return GroupResult{}, err
	}
	return out, nil
}

// PatchGroup applies a SCIM Group PatchOp (AC-2 Patch). It honors the
// {displayName, members} allow-list via the pure PlanGroupPatch planner.
// Affected users = the members actually added or removed (for re-derivation).
func (s *GroupStore) PatchGroup(ctx context.Context, tenantID string, id uuid.UUID, ops []PatchOperation) (GroupResult, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return GroupResult{}, err
	}
	intent, perr := PlanGroupPatch(ops)
	if perr != nil {
		return GroupResult{}, perr
	}
	var out GroupResult
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetSCIMGroupByID(ctx, dbx.GetSCIMGroupByIDParams{
			TenantID: tID, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrGroupNotFound
			}
			return gErr
		}
		current := row
		if intent.SetDisplayName {
			current, gErr = q.ReplaceSCIMGroupDisplayName(ctx, dbx.ReplaceSCIMGroupDisplayNameParams{
				TenantID: tID, ID: row.ID, DisplayName: intent.DisplayName,
			})
			if gErr != nil {
				return fmt.Errorf("scim: patch group display_name: %w", gErr)
			}
		}
		g := domainGroupFromRow(current)
		groupRef := g.GroupRef()
		affected := map[string]struct{}{}

		if intent.ReplaceMembers {
			prev, mErr := q.ListSCIMGroupMembers(ctx, dbx.ListSCIMGroupMembersParams{
				TenantID: tID, GroupID: row.ID,
			})
			if mErr != nil {
				return mErr
			}
			if _, dErr := q.RemoveAllSCIMGroupMembers(ctx, dbx.RemoveAllSCIMGroupMembersParams{
				TenantID: tID, GroupID: row.ID,
			}); dErr != nil {
				return fmt.Errorf("scim: clear group members: %w", dErr)
			}
			want := dedupeStrings(intent.ReplaceMemberSet)
			for _, uid := range want {
				if aErr := q.AddSCIMGroupMember(ctx, dbx.AddSCIMGroupMemberParams{
					ID:       pgtype.UUID{Bytes: uuid.New(), Valid: true},
					TenantID: tID, GroupID: row.ID, UserID: uid, GroupRef: groupRef,
				}); aErr != nil {
					return fmt.Errorf("scim: add group member: %w", aErr)
				}
			}
			for _, uid := range symmetricDiff(prev, want) {
				affected[uid] = struct{}{}
			}
		} else {
			for _, uid := range dedupeStrings(intent.AddMembers) {
				if aErr := q.AddSCIMGroupMember(ctx, dbx.AddSCIMGroupMemberParams{
					ID:       pgtype.UUID{Bytes: uuid.New(), Valid: true},
					TenantID: tID, GroupID: row.ID, UserID: uid, GroupRef: groupRef,
				}); aErr != nil {
					return fmt.Errorf("scim: add group member: %w", aErr)
				}
				affected[uid] = struct{}{}
			}
			for _, uid := range dedupeStrings(intent.RemoveMembers) {
				n, dErr := q.RemoveSCIMGroupMember(ctx, dbx.RemoveSCIMGroupMemberParams{
					TenantID: tID, GroupID: row.ID, UserID: uid,
				})
				if dErr != nil {
					return fmt.Errorf("scim: remove group member: %w", dErr)
				}
				if n > 0 {
					affected[uid] = struct{}{}
				}
			}
		}

		members, lErr := q.ListSCIMGroupMembers(ctx, dbx.ListSCIMGroupMembersParams{
			TenantID: tID, GroupID: row.ID,
		})
		if lErr != nil {
			return lErr
		}
		out = GroupResult{Group: g, MemberIDs: members, AffectedUsers: keys(affected)}
		return nil
	})
	if err != nil {
		return GroupResult{}, err
	}
	return out, nil
}

// DeleteGroup soft-disables the group (active=false) and clears its membership
// (AC-2 Delete). The row is retained (invariant #2). Every former member is an
// affected user (their membership in this group is gone → re-derive).
func (s *GroupStore) DeleteGroup(ctx context.Context, tenantID string, id uuid.UUID) ([]string, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return nil, err
	}
	var affected []string
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetSCIMGroupByID(ctx, dbx.GetSCIMGroupByIDParams{
			TenantID: tID, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrGroupNotFound
			}
			return gErr
		}
		prev, mErr := q.ListSCIMGroupMembers(ctx, dbx.ListSCIMGroupMembersParams{
			TenantID: tID, GroupID: row.ID,
		})
		if mErr != nil {
			return mErr
		}
		if _, sErr := q.SetSCIMGroupActive(ctx, dbx.SetSCIMGroupActiveParams{
			TenantID: tID, ID: row.ID, Active: false,
		}); sErr != nil {
			return fmt.Errorf("scim: delete group (disable): %w", sErr)
		}
		if _, dErr := q.RemoveAllSCIMGroupMembers(ctx, dbx.RemoveAllSCIMGroupMembersParams{
			TenantID: tID, GroupID: row.ID,
		}); dErr != nil {
			return fmt.Errorf("scim: clear group members on delete: %w", dErr)
		}
		affected = prev
		return nil
	})
	if err != nil {
		return nil, err
	}
	return affected, nil
}

// GroupRefsForUser returns the DISTINCT group_refs the user is currently a
// member of across all active groups (the resolver input on a membership
// change — the user's FULL validated group set, AC-3). Exposed so the handler
// can build the Derive input without re-implementing the join (P0-733-1).
func (s *GroupStore) GroupRefsForUser(ctx context.Context, tenantID, userID string) ([]string, error) {
	tID, err := uuidToPg(tenantID)
	if err != nil {
		return nil, err
	}
	var refs []string
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, lErr := q.ListGroupRefsForUser(ctx, dbx.ListGroupRefsForUserParams{
			TenantID: tID, UserID: userID,
		})
		if lErr != nil {
			return lErr
		}
		refs = rows
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

// --- internals ---

func (s *GroupStore) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *GroupStore) inTxReadOnly(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func domainGroupFromRow(row dbx.ScimGroup) DomainGroup {
	g := DomainGroup{
		ID:          uuid.UUID(row.ID.Bytes),
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		DisplayName: row.DisplayName,
		Active:      row.Active,
	}
	if row.ScimExternalID != nil {
		g.ExternalID = *row.ScimExternalID
	}
	if row.CreatedAt.Valid {
		g.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		g.UpdatedAt = row.UpdatedAt.Time
	}
	return g
}

// symmetricDiff returns the union of (prev \ want) and (want \ prev) — every
// user whose membership flipped. De-duplicated.
func symmetricDiff(prev, want []string) []string {
	prevSet := make(map[string]struct{}, len(prev))
	for _, p := range prev {
		prevSet[p] = struct{}{}
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, w := range want {
		wantSet[w] = struct{}{}
	}
	out := make([]string, 0)
	for _, w := range want {
		if _, ok := prevSet[w]; !ok {
			out = append(out, w)
		}
	}
	for _, p := range prev {
		if _, ok := wantSet[p]; !ok {
			out = append(out, p)
		}
	}
	return dedupeStrings(out)
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
