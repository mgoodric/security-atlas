// Package notifications owns the slice 029 notifications spine -- the
// in-app notification dispatch surface and the /v1/me/notifications
// read surface.
//
// The dispatch path is invoked synchronously from the audit-notes
// handler after a successful audit_note insert (in a separate
// transaction); the read path serves the caller's notifications via
// GET /v1/me/notifications + PATCH /v1/me/notifications/{id}/read.
//
// Constitutional invariants honored:
//
//	#6  Tenant isolation via RLS (FORCE) + tenant GUC applied at tx start.
//
// Note shape preservation for slice 030: the OSCAL `observation`
// annotation maps audit-note rows to OSCAL artifacts; the notifications
// table is a separate operational concern and is NOT exported to OSCAL.
package notifications

import (
	"context"
	"encoding/json"
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

// Type constants for notification.type. New notification types must
// register here so the wire layer can render type-specific UI.
const (
	TypeAuditNoteReply = "audit_note.reply"
)

// ErrNotFound is returned for cross-recipient, cross-tenant, or absent
// lookups (mark-read).
var ErrNotFound = errors.New("notifications: not found")

// Notification is the public wire shape.
type Notification struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	RecipientUserID string
	Type            string
	Payload         map[string]any
	CreatedAt       time.Time
	ReadAt          *time.Time
}

// AuditNotePayload is the shape carried by TypeAuditNoteReply
// notifications. JSON-serialized into the notifications.payload column.
type AuditNotePayload struct {
	AuditNoteID   string `json:"audit_note_id"`
	AuditPeriodID string `json:"audit_period_id"`
	ScopeType     string `json:"scope_type"`
	ScopeID       string `json:"scope_id,omitempty"`
	AuthorUserID  string `json:"author_user_id"`
}

// Store is the entry point for notifications read/write operations.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires the Store. Pool is held but not owned.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// DispatchAuditNoteReply fires one notification per recipient for a new
// audit-note. Empty recipients list is a no-op (single-author thread).
//
// Dispatch is best-effort from the audit-notes handler's perspective:
// the audit_note has already committed when this is called, so any
// failure here is logged but does NOT cascade back to the caller. We
// chose this over an in-transaction dispatch because the canvas treats
// audit_notes as the authoritative artifact and notifications as the
// operational courtesy -- it's better to lose a notification than lose
// the auditor's comment.
func (s *Store) DispatchAuditNoteReply(ctx context.Context, recipients []string, payload AuditNotePayload) ([]Notification, error) {
	if len(recipients) == 0 {
		return nil, nil
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("notifications: marshal payload: %w", err)
	}

	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, fmt.Errorf("notifications: parse tenant id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("notifications: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)

	out := make([]Notification, 0, len(recipients))
	for _, rec := range recipients {
		if rec == "" {
			continue
		}
		row, err := q.CreateNotification(ctx, dbx.CreateNotificationParams{
			ID:              pgUUID(uuid.New()),
			TenantID:        pgUUID(tenantID),
			RecipientUserID: rec,
			Type:            TypeAuditNoteReply,
			Payload:         rawPayload,
		})
		if err != nil {
			return nil, fmt.Errorf("notifications: insert for %q: %w", rec, err)
		}
		n, err := fromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("notifications: commit: %w", err)
	}
	return out, nil
}

// ListForRecipient returns the caller's notifications, unread first.
// limit and offset implement basic paging; the caller passes them
// directly from the request.
func (s *Store) ListForRecipient(ctx context.Context, recipient string, limit, offset int32) ([]Notification, int64, error) {
	if recipient == "" {
		return nil, 0, fmt.Errorf("notifications: recipient must be non-empty")
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var out []Notification
	var unread int64
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListNotificationsForUser(ctx, dbx.ListNotificationsForUserParams{
			TenantID:        pgUUID(tenantID),
			RecipientUserID: recipient,
			Limit:           limit,
			Offset:          offset,
		})
		if err != nil {
			return fmt.Errorf("notifications: list: %w", err)
		}
		out = make([]Notification, 0, len(rows))
		for _, r := range rows {
			n, err := fromRow(r)
			if err != nil {
				return err
			}
			out = append(out, n)
		}
		count, err := q.CountUnreadNotificationsForUser(ctx, dbx.CountUnreadNotificationsForUserParams{
			TenantID:        pgUUID(tenantID),
			RecipientUserID: recipient,
		})
		if err != nil {
			return fmt.Errorf("notifications: count unread: %w", err)
		}
		unread = count
		return nil
	})
	return out, unread, err
}

// MarkRead marks a notification as read for the given recipient.
// Cross-recipient / cross-tenant / absent rows return ErrNotFound.
func (s *Store) MarkRead(ctx context.Context, id uuid.UUID, recipient string) (Notification, error) {
	if recipient == "" {
		return Notification{}, fmt.Errorf("notifications: recipient must be non-empty")
	}
	var out Notification
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.MarkNotificationRead(ctx, dbx.MarkNotificationReadParams{
			TenantID:        pgUUID(tenantID),
			RecipientUserID: recipient,
			ID:              pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("notifications: mark read: %w", err)
		}
		n, err := fromRow(row)
		if err != nil {
			return err
		}
		out = n
		return nil
	})
	return out, err
}

// inTx mirrors the slice-028 period.Store pattern.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("notifications: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("notifications: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("notifications: commit: %w", err)
	}
	return nil
}

func fromRow(r dbx.Notification) (Notification, error) {
	n := Notification{
		ID:              uuid.UUID(r.ID.Bytes),
		TenantID:        uuid.UUID(r.TenantID.Bytes),
		RecipientUserID: r.RecipientUserID,
		Type:            r.Type,
	}
	if len(r.Payload) > 0 {
		if err := json.Unmarshal(r.Payload, &n.Payload); err != nil {
			return Notification{}, fmt.Errorf("notifications: unmarshal payload: %w", err)
		}
	} else {
		n.Payload = map[string]any{}
	}
	if r.CreatedAt.Valid {
		n.CreatedAt = r.CreatedAt.Time
	}
	if r.ReadAt.Valid {
		t := r.ReadAt.Time
		n.ReadAt = &t
	}
	return n, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
