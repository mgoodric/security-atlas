// Package userprefs persists per-user notification preferences for /v1/me/preferences.
//
// Storage model (slice 108 D3): one row per (tenant, user, event, channel) tuple. A
// user with no rows reads as all-enabled per the default-on-missing-row policy
// documented in AC-4. The PATCH partial-merge semantic (AC-5) is the natural primitive
// of the UPSERT query.
//
// Event + channel taxonomies are intentionally narrow whitelists. The schema's CHECK
// constraints + this package's whitelist guards must move together — extending one
// without the other strands the surface in an invalid state.
package userprefs

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

// ErrUnknownEvent is the sentinel for "the supplied event key is not in the whitelist."
// The HTTP layer surfaces 400 Bad Request on this sentinel.
var ErrUnknownEvent = errors.New("userprefs: unknown event")

// ErrUnknownChannel is the sentinel for "the supplied channel key is not in the
// whitelist." Same 400 mapping as ErrUnknownEvent.
var ErrUnknownChannel = errors.New("userprefs: unknown channel")

// Events is the canonical event whitelist. Mirrors the migration's CHECK constraint.
// When a new event is added it MUST land in BOTH places in the same slice.
var Events = []string{
	"audit_period_assignment",
	"policy_ack_due",
	"risk_review_overdue",
	"control_drift",
}

// Channels is the canonical channel whitelist. Mirrors the migration's CHECK constraint.
var Channels = []string{"in_app", "email"}

// Preferences is the matrix shape returned by Get + accepted (partially) by Upsert.
// Outer key = event; inner key = channel; bool = enabled. Missing keys are treated as
// the default (enabled=true) by the read path.
type Preferences map[string]map[string]bool

// Store wraps the user_notification_preferences table with tenancy plumbing.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Get returns the caller's preference matrix with default-fills for missing rows.
// A fresh user (zero rows) reads as all-events × all-channels = enabled=true.
func (s *Store) Get(ctx context.Context, tenantID, userID uuid.UUID) (Preferences, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	rows, err := q.ListUserNotificationPreferences(ctx, dbx.ListUserNotificationPreferencesParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("userprefs: list: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := DefaultMatrix()
	for _, r := range rows {
		if _, ok := out[r.Event]; !ok {
			out[r.Event] = map[string]bool{}
		}
		out[r.Event][r.Channel] = r.Enabled
	}
	return out, nil
}

// Upsert applies a partial matrix (merge semantics). Returns ErrUnknownEvent or
// ErrUnknownChannel on the first key that isn't in the whitelist — the caller maps
// either sentinel to 400 Bad Request. Each cell is its own atomic upsert so a partial
// failure leaves the prior cells written (PATCH is not transactional across cells —
// this matches the "merge per-cell" promise of AC-5).
func (s *Store) Upsert(ctx context.Context, tenantID, userID uuid.UUID, in Preferences) error {
	// Validate the entire input BEFORE the first write so an unknown key on cell 3
	// of 8 doesn't leave cells 1–2 written. Pre-flight check is cheap; the per-cell
	// transaction below carries the actual write.
	for event, channels := range in {
		if !isAllowedEvent(event) {
			return fmt.Errorf("%w: %q", ErrUnknownEvent, event)
		}
		for channel := range channels {
			if !isAllowedChannel(channel) {
				return fmt.Errorf("%w: %q", ErrUnknownChannel, channel)
			}
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	for event, channels := range in {
		for channel, enabled := range channels {
			if err := q.UpsertUserNotificationPreference(ctx, dbx.UpsertUserNotificationPreferenceParams{
				TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
				UserID:   pgtype.UUID{Bytes: userID, Valid: true},
				Event:    event,
				Channel:  channel,
				Enabled:  enabled,
			}); err != nil {
				return fmt.Errorf("userprefs: upsert (%s/%s): %w", event, channel, err)
			}
		}
	}
	return tx.Commit(ctx)
}

// DefaultMatrix returns the fully-populated default matrix (every event × every
// channel = enabled). Used by Get to fill in missing rows and by tests as the
// "fresh user" expectation.
func DefaultMatrix() Preferences {
	out := make(Preferences, len(Events))
	for _, ev := range Events {
		cells := make(map[string]bool, len(Channels))
		for _, ch := range Channels {
			cells[ch] = true
		}
		out[ev] = cells
	}
	return out
}

func isAllowedEvent(e string) bool {
	for _, x := range Events {
		if x == e {
			return true
		}
	}
	return false
}

func isAllowedChannel(c string) bool {
	for _, x := range Channels {
		if x == c {
			return true
		}
	}
	return false
}
