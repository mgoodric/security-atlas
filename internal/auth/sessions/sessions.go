// Package sessions persists server-side opaque sessions.
//
// A session is a row in `sessions` keyed on a 256-bit random id; the same
// id string is the value of the `atlas_session` cookie. Sliding-window
// refresh extends a session whose remaining lifetime falls below
// RefreshThreshold. Logout sets revoked_at; subsequent reads return
// ErrRevoked.
package sessions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// CookieName is the session cookie. HttpOnly + Secure + SameSite=Lax.
	CookieName = "atlas_session"

	// DefaultTTL is the lifetime of a freshly-issued session.
	DefaultTTL = 7 * 24 * time.Hour

	// RefreshThreshold is how close to expires_at the session must be to
	// trigger a sliding-window refresh on Touch.
	RefreshThreshold = 24 * time.Hour

	// idBytes is the raw size of the random session id; base64-url makes
	// it 43 characters long. 256 bits is comfortably more entropy than
	// any practical attacker can search.
	idBytes = 32
)

// ErrNotFound is returned when no session exists for the supplied id.
var ErrNotFound = errors.New("sessions: not found")

// ErrRevoked is returned when the session row exists but revoked_at is set.
var ErrRevoked = errors.New("sessions: revoked")

// ErrExpired is returned when expires_at < now.
var ErrExpired = errors.New("sessions: expired")

// Session is the domain projection.
type Session struct {
	ID         string
	TenantID   uuid.UUID
	UserID     uuid.UUID
	IdpIssuer  string
	IdpSubject string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
}

// Store wraps the sessions table with tenancy plumbing.
type Store struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

// NewStore constructs a Store. ttl defaults to DefaultTTL when zero.
func NewStore(pool *pgxpool.Pool, ttl time.Duration) *Store {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return &Store{pool: pool, ttl: ttl}
}

// NewID returns a fresh 256-bit random session id, URL-base64 encoded.
func NewID() (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sessions: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateInput captures what the login handlers know after authenticating.
type CreateInput struct {
	TenantID   uuid.UUID
	UserID     uuid.UUID
	IdpIssuer  string
	IdpSubject string
}

// Create persists a new session and returns its id (cookie value).
func (s *Store) Create(ctx context.Context, in CreateInput) (Session, error) {
	id, err := NewID()
	if err != nil {
		return Session{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Session{}, err
	}
	q := dbx.New(tx)
	expiresAt := time.Now().UTC().Add(s.ttl)
	row, err := q.CreateSession(ctx, dbx.CreateSessionParams{
		ID:         id,
		TenantID:   pgtype.UUID{Bytes: in.TenantID, Valid: true},
		UserID:     pgtype.UUID{Bytes: in.UserID, Valid: true},
		IdpIssuer:  in.IdpIssuer,
		IdpSubject: in.IdpSubject,
		ExpiresAt:  pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return Session{}, fmt.Errorf("sessions: create: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return sessionFromRow(row), nil
}

// Read loads a session by id under a known tenant context. Returns
// ErrNotFound / ErrRevoked / ErrExpired as appropriate.
func (s *Store) Read(ctx context.Context, tenantID uuid.UUID, id string) (Session, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Session{}, err
	}
	q := dbx.New(tx)
	row, err := q.GetSessionByID(ctx, dbx.GetSessionByIDParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		ID:       id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	if row.RevokedAt.Valid {
		return Session{}, ErrRevoked
	}
	if !row.ExpiresAt.Valid || time.Now().UTC().After(row.ExpiresAt.Time) {
		return Session{}, ErrExpired
	}
	// Sliding-window refresh: if close to expiry, extend in-tx.
	if time.Until(row.ExpiresAt.Time) < RefreshThreshold {
		newExpiry := time.Now().UTC().Add(s.ttl)
		if err := q.TouchSession(ctx, dbx.TouchSessionParams{
			TenantID:  pgtype.UUID{Bytes: tenantID, Valid: true},
			ID:        id,
			ExpiresAt: pgtype.Timestamptz{Time: newExpiry, Valid: true},
		}); err != nil {
			return Session{}, err
		}
		row.ExpiresAt = pgtype.Timestamptz{Time: newExpiry, Valid: true}
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return sessionFromRow(row), nil
}

// ListForUser returns every currently-valid session belonging to userID under
// tenantID. "Currently-valid" means revoked_at IS NULL AND expires_at > now().
// Slice 108: backs GET /v1/me/sessions.
func (s *Store) ListForUser(ctx context.Context, tenantID, userID uuid.UUID) ([]Session, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	rows, err := q.ListSessionsForUser(ctx, dbx.ListSessionsForUserParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("sessions: list for user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := make([]Session, len(rows))
	for i, r := range rows {
		out[i] = sessionFromRow(r)
	}
	return out, nil
}

// RevokeForUser revokes a single session iff it belongs to userID. Returns true
// when a matching row was updated. The ownership guard lives in the SQL WHERE
// clause so a cross-user id never updates the wrong row. Idempotent: re-revoking
// an already-revoked row leaves revoked_at unchanged but still returns true (the
// row matched the WHERE). Slice 108: backs DELETE /v1/me/sessions/{id}.
func (s *Store) RevokeForUser(ctx context.Context, tenantID, userID uuid.UUID, id string) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return false, err
	}
	q := dbx.New(tx)
	rowsAffected, err := q.RevokeSessionForUser(ctx, dbx.RevokeSessionForUserParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		ID:       id,
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("sessions: revoke for user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// RevokeOthersForUser revokes every valid session for userID EXCEPT keepID. Pass
// "" for keepID to revoke every session including the current one. Returns the
// count of revoked rows. Slice 108: backs DELETE /v1/me/sessions ("sign out other
// devices").
func (s *Store) RevokeOthersForUser(ctx context.Context, tenantID, userID uuid.UUID, keepID string) (int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return 0, err
	}
	q := dbx.New(tx)
	n, err := q.RevokeOtherSessionsForUser(ctx, dbx.RevokeOtherSessionsForUserParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
		ID:       keepID,
	})
	if err != nil {
		return 0, fmt.Errorf("sessions: revoke others: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

// Revoke flags a session as logged-out. Idempotent.
func (s *Store) Revoke(ctx context.Context, tenantID uuid.UUID, id string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := q.RevokeSession(ctx, dbx.RevokeSessionParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		ID:       id,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetCookie sets the atlas_session cookie. HttpOnly + Secure + SameSite=Lax
// + Path=/. The Secure flag is honored when the request is HTTPS; tests
// that mount under http://localhost can pass secureOverride=false.
func SetCookie(w http.ResponseWriter, id string, expiresAt time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie deletes the atlas_session cookie by setting MaxAge=-1.
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func sessionFromRow(row dbx.Session) Session {
	s := Session{
		ID:         row.ID,
		TenantID:   uuid.UUID(row.TenantID.Bytes),
		UserID:     uuid.UUID(row.UserID.Bytes),
		IdpIssuer:  row.IdpIssuer,
		IdpSubject: row.IdpSubject,
	}
	if row.IssuedAt.Valid {
		s.IssuedAt = row.IssuedAt.Time
	}
	if row.ExpiresAt.Valid {
		s.ExpiresAt = row.ExpiresAt.Time
	}
	if row.LastSeenAt.Valid {
		s.LastSeenAt = row.LastSeenAt.Time
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		s.RevokedAt = &t
	}
	return s
}
