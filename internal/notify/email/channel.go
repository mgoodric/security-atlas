package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DeliveryResult reports the outcome of a delivery attempt for one user.
type DeliveryResult struct {
	// Sent is true when a digest was actually transmitted this call.
	Sent bool
	// Skipped is true when there was nothing to send (no unread, opted
	// out, or already delivered this period).
	Skipped bool
	// Reason is a short human-readable explanation for a skip.
	Reason string
}

// Channel is the delivery orchestrator. It reads the opted-in target
// user's notifications under the user's OWN tenant context (RLS-scoped),
// builds a minimum-disclosure digest, claims the digest idempotently, and
// delivers to the user's ACCOUNT EMAIL only (P0-445-1/3).
//
// It is a SINK: it never writes a notification (P0-445-5).
type Channel struct {
	pool     *pgxpool.Pool
	provider Provider
	baseURL  string
	now      func() time.Time
}

// NewChannel wires the channel. baseURL is the public app base URL for the
// digest deep-link (typically Config.BaseURL).
func NewChannel(pool *pgxpool.Pool, provider Provider, baseURL string) *Channel {
	return &Channel{
		pool:     pool,
		provider: provider,
		baseURL:  baseURL,
		now:      time.Now,
	}
}

// SetEmailOptIn sets the caller's email-channel master opt-in (AC-9).
// Default is opted-out; this is the only path that flips it on. The
// tenant + user are taken from the authenticated context (no
// user-controlled recipient or cross-user path — P0-445-1, E).
func (c *Channel) SetEmailOptIn(ctx context.Context, tenantID, userID uuid.UUID, enabled bool) error {
	return c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		_, err := q.UpsertEmailOptIn(ctx, dbx.UpsertEmailOptInParams{
			TenantID: pgUUID(tenantID),
			UserID:   pgUUID(userID),
			Enabled:  enabled,
		})
		if err != nil {
			return fmt.Errorf("email: upsert opt-in: %w", err)
		}
		return nil
	})
}

// GetEmailOptIn reports whether the caller has opted in. A missing row is
// opted-OUT (P0-445-7).
func (c *Channel) GetEmailOptIn(ctx context.Context, tenantID, userID uuid.UUID) (bool, error) {
	var enabled bool
	err := c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		v, err := q.GetEmailOptIn(ctx, dbx.GetEmailOptInParams{
			TenantID: pgUUID(tenantID),
			UserID:   pgUUID(userID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			enabled = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("email: get opt-in: %w", err)
		}
		enabled = v
		return nil
	})
	return enabled, err
}

// DeliverDigest builds + delivers the unread-notification digest for one
// target user. The ctx MUST already carry the user's tenant (set by the
// caller via tenancy.WithTenant); the channel reads everything under that
// tenant context, so Tenant A's notifications can NEVER reach Tenant B's
// user (AC-13 / P0-445-3).
//
//	userID          — the tenant-scoped target user (UUID form, for the
//	                  users-table account-email lookup).
//	recipientUserID — the slice-029 string user-id (notifications.recipient_user_id).
//
// Flow: opt-in check → unread fetch → claim (idempotent) → resolve account
// email → build minimum-disclosure digest → send → record outcome.
func (c *Channel) DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (DeliveryResult, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return DeliveryResult{}, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return DeliveryResult{}, fmt.Errorf("email: parse tenant id: %w", err)
	}

	optedIn, err := c.GetEmailOptIn(ctx, tenantID, userID)
	if err != nil {
		return DeliveryResult{}, err
	}
	if !optedIn {
		return DeliveryResult{Skipped: true, Reason: "user opted out"}, nil
	}

	digestKey := DigestKeyForDay(c.now())

	var result DeliveryResult
	// Phase 1: read unread counts + claim the digest + resolve email,
	// all under one tenant-scoped tx.
	var msg Message
	var claimID pgtype.UUID
	var claimed bool
	err = c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, err := q.ListNotificationsForUser(ctx, dbx.ListNotificationsForUserParams{
			TenantID:        pgUUID(tenantID),
			RecipientUserID: recipientUserID,
			Limit:           500,
			Offset:          0,
		})
		if err != nil {
			return fmt.Errorf("email: list notifications: %w", err)
		}
		counts := map[string]int{}
		unread := 0
		for _, r := range rows {
			if r.ReadAt.Valid {
				continue // digest is unread-only
			}
			counts[r.Type]++
			unread++
		}
		if unread == 0 {
			result = DeliveryResult{Skipped: true, Reason: "no unread notifications"}
			return nil
		}

		// Claim the digest BEFORE building/sending (idempotency, AC-5).
		id, err := q.ClaimEmailDigest(ctx, dbx.ClaimEmailDigestParams{
			TenantID:        pgUUID(tenantID),
			RecipientUserID: recipientUserID,
			DigestKey:       digestKey,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			result = DeliveryResult{Skipped: true, Reason: "already delivered this period"}
			return nil
		}
		if err != nil {
			return fmt.Errorf("email: claim digest: %w", err)
		}
		claimID = id
		claimed = true

		// Resolve the ACCOUNT email (P0-445-1: no user-controlled recipient).
		usr, err := q.GetUserByID(ctx, dbx.GetUserByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(userID),
		})
		if err != nil {
			return fmt.Errorf("email: resolve account email: %w", err)
		}

		msg, err = BuildDigest(DigestInput{
			Recipient:   usr.Email,
			BaseURL:     c.baseURL,
			TypeCounts:  counts,
			TotalUnread: unread,
		})
		if err != nil {
			return fmt.Errorf("email: build digest: %w", err)
		}
		return nil
	})
	if err != nil {
		return DeliveryResult{}, err
	}
	if !claimed {
		return result, nil // skipped (no unread / already delivered)
	}

	// Phase 2: send OUTSIDE the read tx (SMTP I/O must not hold a DB tx).
	sendErr := c.provider.Send(ctx, msg)

	// Phase 3: record outcome in its own tenant-scoped tx (AC-8).
	recErr := c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if sendErr != nil {
			return q.MarkEmailDigestFailed(ctx, dbx.MarkEmailDigestFailedParams{
				TenantID:  pgUUID(tenantID),
				ID:        claimID,
				LastError: truncErr(sendErr),
			})
		}
		return q.MarkEmailDigestSent(ctx, dbx.MarkEmailDigestSentParams{
			TenantID: pgUUID(tenantID),
			ID:       claimID,
		})
	})
	if sendErr != nil {
		// The digest stays claimed=failed; the next tick can re-attempt
		// (D8 — no hot retry here).
		return DeliveryResult{}, fmt.Errorf("email: send failed: %w", sendErr)
	}
	if recErr != nil {
		return DeliveryResult{}, fmt.Errorf("email: record outcome: %w", recErr)
	}
	return DeliveryResult{Sent: true}, nil
}

// inTx runs fn in a tenant-scoped transaction (tenant GUC applied at tx
// start). Mirrors the slice-029 notifications.Store.inTx pattern.
func (c *Channel) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("email: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("email: commit: %w", err)
	}
	return nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// truncErr bounds the last_error string written to the delivery log and
// strips control chars (no credential leakage — the SMTP server response
// is safe, but we never echo a multi-line auth dump verbatim).
func truncErr(err error) string {
	s := stripHeaderValue(err.Error())
	const max = 500
	if len(s) > max {
		return s[:max]
	}
	return s
}
