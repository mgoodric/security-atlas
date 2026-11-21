package policy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// auditActionControlsUpdated is the policy_audit_log action for a change
// to a synced policy's control links (#2852).
const auditActionControlsUpdated = "controls_updated"

// SetSyncedLinks replaces the control links of a published synced policy
// in place (#2852). A synced policy's text, version and review date come
// from its page, but its control links are kept in security-atlas, and no
// version is written for them. Duplicate ids count once. Every id must be
// a control in the tenant (ErrUnknownLinkedControl). A change appends one
// controls_updated policy_audit_log row naming the actor and the links
// before and after, in the same transaction; replacing the links with the
// same set writes nothing. ErrNotFound when the id does not resolve;
// ErrNotSyncedPolicy for any row that is not a published synced policy.
func (s *Store) SetSyncedLinks(ctx context.Context, id uuid.UUID, links []uuid.UUID, actor string) (Policy, error) {
	if actor == "" {
		return Policy{}, ErrCreatedByRequired
	}
	next := dedupeUUIDs(links)
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		cur, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("set synced links: get policy: %w", err)
		}
		if cur.SourceAttribution != SourceSynced || cur.Status != StatePublished {
			return ErrNotSyncedPolicy
		}
		if err := checkLinkedControls(ctx, q, tenantID, next); err != nil {
			return err
		}
		if sameUUIDSet(uuidsFromPg(cur.LinkedControlIds), next) {
			out = policyFromRow(cur)
			return nil
		}
		row, err := q.UpdateSyncedPolicyLinks(ctx, dbx.UpdateSyncedPolicyLinksParams{
			TenantID:         pgUUID(tenantID),
			ID:               pgUUID(id),
			LinkedControlIds: uuidsToPg(next),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotSyncedPolicy
			}
			return fmt.Errorf("set synced links: %w", err)
		}
		prev := cur.LinkedControlIds
		if prev == nil {
			prev = []pgtype.UUID{}
		}
		if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
			TenantID:                 pgUUID(tenantID),
			PolicyID:                 row.ID,
			Action:                   auditActionControlsUpdated,
			Actor:                    actor,
			PolicyTitle:              &row.Title,
			PolicyVersion:            &row.Version,
			PreviousLinkedControlIds: prev,
			NewLinkedControlIds:      uuidsToPg(next),
		}); err != nil {
			return fmt.Errorf("set synced links: audit log: %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// dedupeUUIDs drops repeated ids, keeping first-seen order. Never nil.
func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// sameUUIDSet reports whether a and b hold the same distinct ids, in any
// order.
func sameUUIDSet(a, b []uuid.UUID) bool {
	as := make(map[uuid.UUID]struct{}, len(a))
	for _, id := range a {
		as[id] = struct{}{}
	}
	bs := make(map[uuid.UUID]struct{}, len(b))
	for _, id := range b {
		bs[id] = struct{}{}
	}
	if len(as) != len(bs) {
		return false
	}
	for id := range bs {
		if _, ok := as[id]; !ok {
			return false
		}
	}
	return true
}
