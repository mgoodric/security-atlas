// store.go — the OE-670 package-local read layer over the OE-628
// access-review tables.
//
// The OE-628 store (internal/accessreview) exposes CreateCampaign /
// Attest / Rollup / RevokeList / Complete and this slice must not
// change its semantics, so the reads it lacks — a campaign index, a
// single-campaign load, the item listing, and the reviewer-assignment
// lookup the 403-vs-404 mapping needs — live here instead, following
// the internal/api/personnelsecurity (OE-663) precedent for API-owned
// reads over another slice's tables. Read-only: every query is a
// SELECT.
//
// Tenant isolation is enforced at the DB layer via slice-033 RLS. The
// ReadStore opens a transaction per call and applies the tenant GUC via
// internal/tenancy; the application-side `WHERE tenant_id = $1`
// predicate in the SQL is the primary guarantee, RLS is defense in
// depth — identical posture to the OE-628 store's inTx.

package accessreviews

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/accessreview"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ReadStore is the read-only seam. It holds no write methods.
type ReadStore struct {
	pool *pgxpool.Pool
}

// NewReadStore wires a ReadStore over the application pgx pool. The
// pool MUST connect as the application role (NOSUPERUSER NOBYPASSRLS)
// so RLS is actually enforced.
func NewReadStore(pool *pgxpool.Pool) *ReadStore {
	if pool == nil {
		panic("access review api: pool is required")
	}
	return &ReadStore{pool: pool}
}

const campaignColumns = `id, tenant_id, name, source, scope_systems, scope_entitlements,
	scope_user_ids, status, due_at, created_by, completed_at,
	evidence_record_id, created_at, updated_at`

const itemColumns = `id, campaign_id, system, entitlement, principal_user_id,
	principal_email, reviewer_id, status, decision, reason,
	attested_by, attested_at, source, source_ref`

// List returns the tenant's campaigns, newest first. An empty status
// means no filter; the handler validates the value before calling.
func (s *ReadStore) List(ctx context.Context, status string) ([]accessreview.Campaign, error) {
	var out []accessreview.Campaign
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT `+campaignColumns+`
			FROM access_review_campaigns
			WHERE tenant_id = $1 AND ($2 = '' OR status = $2)
			ORDER BY created_at DESC, id
		`, tenantID, status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c accessreview.Campaign
			if err := scanCampaign(rows, &c); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// Get returns one campaign or accessreview.ErrNotFound. A campaign
// belonging to another tenant is indistinguishable from a missing one.
func (s *ReadStore) Get(ctx context.Context, campaignID uuid.UUID) (accessreview.Campaign, error) {
	var out accessreview.Campaign
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		row := tx.QueryRow(ctx, `
			SELECT `+campaignColumns+`
			FROM access_review_campaigns
			WHERE tenant_id = $1 AND id = $2
		`, tenantID, campaignID)
		if err := scanCampaign(row, &out); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return accessreview.ErrNotFound
			}
			return err
		}
		return nil
	})
	return out, err
}

// Items returns a campaign's review items in the deterministic
// (system, entitlement, principal) order the revoke-list export uses.
func (s *ReadStore) Items(ctx context.Context, campaignID uuid.UUID) ([]accessreview.Item, error) {
	var out []accessreview.Item
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT `+itemColumns+`
			FROM access_review_items
			WHERE tenant_id = $1 AND campaign_id = $2
			ORDER BY system, entitlement, principal_email, principal_user_id, id
		`, tenantID, campaignID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item accessreview.Item
			if err := scanItem(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

// ItemReviewer returns the reviewer assigned to one review item, or
// accessreview.ErrNotFound. The campaign id is part of the predicate so
// an item id under the wrong campaign path is a 404, not a hit — and a
// cross-tenant item is indistinguishable from a missing one.
func (s *ReadStore) ItemReviewer(ctx context.Context, campaignID, itemID uuid.UUID) (string, error) {
	var reviewer string
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		err := tx.QueryRow(ctx, `
			SELECT reviewer_id
			FROM access_review_items
			WHERE tenant_id = $1 AND campaign_id = $2 AND id = $3
		`, tenantID, campaignID, itemID).Scan(&reviewer)
		if errors.Is(err, pgx.ErrNoRows) {
			return accessreview.ErrNotFound
		}
		return err
	})
	return reviewer, err
}

func (s *ReadStore) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("access review api: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access review api: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanCampaign(row pgx.Row, out *accessreview.Campaign) error {
	return row.Scan(&out.ID, &out.TenantID, &out.Name, &out.Source, &out.Scope.Systems,
		&out.Scope.Entitlements, &out.Scope.UserIDs, &out.Status, &out.DueAt,
		&out.CreatedBy, &out.CompletedAt, &out.EvidenceRecordID, &out.CreatedAt, &out.UpdatedAt)
}

func scanItem(row pgx.Row, out *accessreview.Item) error {
	return row.Scan(&out.ID, &out.CampaignID, &out.System, &out.Entitlement,
		&out.PrincipalUserID, &out.PrincipalEmail, &out.ReviewerID, &out.Status,
		&out.Decision, &out.Reason, &out.AttestedBy, &out.AttestedAt, &out.Source, &out.SourceRef)
}
