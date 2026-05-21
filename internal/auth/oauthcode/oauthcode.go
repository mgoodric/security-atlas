// Package oauthcode is the DB-backed store for OAuth authorization
// codes (RFC 6749 §4.1) and per-client registered redirect URIs (RFC
// 6749 §10.6).
//
// Slice 189 ships two surfaces:
//
//   - Authorization codes (`oauth_auth_codes`) — short-lived, one-shot
//     codes that bridge `GET /oauth/authorize` to `POST /oauth/token`.
//     The store enforces single-use via UPDATE ... WHERE consumed_at
//     IS NULL RETURNING.
//   - Redirect-URI registry (`oauth_client_redirect_uris`) — the
//     open-redirect prevention gate for the authorize handler.
//
// Tenancy: NEITHER table is tenant-scoped (see migration headers for
// rationale). Connection contexts here do NOT need a tenant GUC; the
// pool used is the atlas_app pool but RLS does not apply to these
// tables.
//
// Anti-criteria honored:
//
//   - P0-189-1 (PKCE S256 only): the DB CHECK enforces the column
//     value; this package additionally rejects any other method
//     surfaced via Insert.
//   - P0-189-3 (one-shot codes): ConsumeOnce uses UPDATE RETURNING
//     under the `consumed_at IS NULL` predicate; a second attempt
//     returns 0 rows.
package oauthcode

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgErrUniqueViolation = "23505"

// DefaultTTL is the authorization-code TTL per OAuth 2.1 §4.1.3
// recommendation. The handler computes expires_at = created_at +
// DefaultTTL at issuance time; the redemption path checks expiry
// before the one-shot consume.
const DefaultTTL = 60 * time.Second

// PKCEMethodS256 is the only supported PKCE code-challenge method.
// `plain` is forbidden at the DB CHECK constraint AND here.
const PKCEMethodS256 = "S256"

// ErrNotFound is returned when no row matched the lookup predicate.
var ErrNotFound = errors.New("oauthcode: not found")

// ErrAlreadyConsumed is returned by ConsumeOnce when the code matched
// but had already been consumed. The token endpoint folds this into
// invalid_grant per RFC 6749 §5.2.
var ErrAlreadyConsumed = errors.New("oauthcode: already consumed")

// ErrExpired is returned by ConsumeOnce when the code matched and was
// unconsumed, but expires_at < now.
var ErrExpired = errors.New("oauthcode: expired")

// ErrDuplicateRedirectURI is returned by RegisterRedirectURI when the
// (client_id, redirect_uri) pair is already registered. The CLI maps
// this to a clean operator-facing error.
var ErrDuplicateRedirectURI = errors.New("oauthcode: redirect_uri already registered for client")

// AuthCode is the in-memory projection of an oauth_auth_codes row.
type AuthCode struct {
	Code                string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	UserID              uuid.UUID
	IDPIssuer           string
	IDPSubject          string
	CurrentTenantID     uuid.UUID
	AvailableTenants    []uuid.UUID
	Roles               []byte // raw JSONB bytes; caller unmarshals as map[uuid.UUID][]string
	SuperAdmin          bool
	CreatedAt           time.Time
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
}

// InsertParams captures everything the authorize handler needs to
// hand off to the redemption path.
type InsertParams struct {
	Code                string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	UserID              uuid.UUID
	IDPIssuer           string
	IDPSubject          string
	CurrentTenantID     uuid.UUID
	AvailableTenants    []uuid.UUID
	RolesJSON           []byte // already-marshalled JSONB; pass `[]byte("{}")` when empty
	SuperAdmin          bool
	TTL                 time.Duration // zero falls back to DefaultTTL
}

// Store wraps a pgxpool with the slice-189 primitives.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool. The pool is the atlas_app pool
// from the caller; RLS does not apply to either of this slice's
// tables (see migration headers). Callers SHOULD NOT call
// tenancy.ApplyTenant before invoking these methods.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: time.Now}
}

// WithClock returns a Store that uses the supplied clock. Tests
// inject a pinned clock for deterministic expiry semantics.
func (s *Store) WithClock(now func() time.Time) *Store {
	cp := *s
	cp.now = now
	return &cp
}

// Insert persists a freshly-minted authorization code. expires_at is
// computed as now + (params.TTL || DefaultTTL).
func (s *Store) Insert(ctx context.Context, p InsertParams) (AuthCode, error) {
	if p.Code == "" {
		return AuthCode{}, errors.New("oauthcode: code is empty")
	}
	if p.CodeChallengeMethod != PKCEMethodS256 {
		return AuthCode{}, fmt.Errorf("oauthcode: code_challenge_method=%q rejected; only %q supported",
			p.CodeChallengeMethod, PKCEMethodS256)
	}
	ttl := p.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if len(p.RolesJSON) == 0 {
		p.RolesJSON = []byte("{}")
	}
	if p.AvailableTenants == nil {
		p.AvailableTenants = []uuid.UUID{}
	}
	now := s.now().UTC()
	expiresAt := now.Add(ttl)

	const q = `
		INSERT INTO oauth_auth_codes (
			code, client_id, redirect_uri,
			code_challenge, code_challenge_method,
			user_id, idp_issuer, idp_subject,
			current_tenant_id, available_tenants, roles, super_admin,
			created_at, expires_at
		) VALUES (
			$1, $2, $3,
			$4, $5,
			$6, $7, $8,
			$9, $10, $11, $12,
			$13, $14
		)
		RETURNING code, client_id, redirect_uri,
		          code_challenge, code_challenge_method,
		          user_id, idp_issuer, idp_subject,
		          current_tenant_id, available_tenants, roles, super_admin,
		          created_at, expires_at, consumed_at
	`
	row := s.pool.QueryRow(ctx, q,
		p.Code, p.ClientID, p.RedirectURI,
		p.CodeChallenge, p.CodeChallengeMethod,
		p.UserID, p.IDPIssuer, p.IDPSubject,
		p.CurrentTenantID, p.AvailableTenants, p.RolesJSON, p.SuperAdmin,
		now, expiresAt,
	)
	var ac AuthCode
	var consumed *time.Time
	if err := row.Scan(
		&ac.Code, &ac.ClientID, &ac.RedirectURI,
		&ac.CodeChallenge, &ac.CodeChallengeMethod,
		&ac.UserID, &ac.IDPIssuer, &ac.IDPSubject,
		&ac.CurrentTenantID, &ac.AvailableTenants, &ac.Roles, &ac.SuperAdmin,
		&ac.CreatedAt, &ac.ExpiresAt, &consumed,
	); err != nil {
		return AuthCode{}, fmt.Errorf("oauthcode: insert: %w", err)
	}
	ac.ConsumedAt = consumed
	return ac, nil
}

// ConsumeOnce atomically marks the code consumed and returns the row.
// One-shot semantics: a second call against the same code returns
// ErrAlreadyConsumed. Codes whose expires_at has passed return
// ErrExpired (and are NOT consumed).
//
// Implementation: a two-step transaction would be racey; we do it in
// one statement with a CTE that filters by `consumed_at IS NULL` and
// returns the row state for the application to check expiry against
// the wall clock.
func (s *Store) ConsumeOnce(ctx context.Context, code string) (AuthCode, error) {
	if code == "" {
		return AuthCode{}, ErrNotFound
	}
	// First peek at the row to distinguish ErrNotFound / ErrExpired /
	// ErrAlreadyConsumed. The actual consume is a second UPDATE under
	// the IS NULL predicate. Two statements but within the same
	// transaction so the peek + consume are atomic w.r.t. concurrent
	// consume attempts.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AuthCode{}, fmt.Errorf("oauthcode: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const peekQ = `
		SELECT code, client_id, redirect_uri,
		       code_challenge, code_challenge_method,
		       user_id, idp_issuer, idp_subject,
		       current_tenant_id, available_tenants, roles, super_admin,
		       created_at, expires_at, consumed_at
		FROM oauth_auth_codes
		WHERE code = $1
		FOR UPDATE
	`
	var ac AuthCode
	var consumed *time.Time
	err = tx.QueryRow(ctx, peekQ, code).Scan(
		&ac.Code, &ac.ClientID, &ac.RedirectURI,
		&ac.CodeChallenge, &ac.CodeChallengeMethod,
		&ac.UserID, &ac.IDPIssuer, &ac.IDPSubject,
		&ac.CurrentTenantID, &ac.AvailableTenants, &ac.Roles, &ac.SuperAdmin,
		&ac.CreatedAt, &ac.ExpiresAt, &consumed,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthCode{}, ErrNotFound
		}
		return AuthCode{}, fmt.Errorf("oauthcode: peek: %w", err)
	}
	if consumed != nil {
		return AuthCode{}, ErrAlreadyConsumed
	}
	now := s.now().UTC()
	if !ac.ExpiresAt.After(now) {
		return AuthCode{}, ErrExpired
	}

	const consumeQ = `
		UPDATE oauth_auth_codes
		SET consumed_at = $2
		WHERE code = $1 AND consumed_at IS NULL
		RETURNING consumed_at
	`
	var consumedAt time.Time
	if err := tx.QueryRow(ctx, consumeQ, code, now).Scan(&consumedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Lost a race with a concurrent consume — collapse to
			// ErrAlreadyConsumed.
			return AuthCode{}, ErrAlreadyConsumed
		}
		return AuthCode{}, fmt.Errorf("oauthcode: consume: %w", err)
	}
	ac.ConsumedAt = &consumedAt
	if err := tx.Commit(ctx); err != nil {
		return AuthCode{}, fmt.Errorf("oauthcode: commit: %w", err)
	}
	return ac, nil
}

// SweepExpired DELETEs auth-code rows whose created_at predates
// `olderThan`. Returns the number of rows removed. The sweeper
// goroutine calls this every 5 minutes with `olderThan = now - 1h`
// (1-hour grace beyond the 60s TTL avoids races with in-flight
// redemptions).
func (s *Store) SweepExpired(ctx context.Context, olderThan time.Time) (int64, error) {
	const q = `DELETE FROM oauth_auth_codes WHERE created_at < $1`
	tag, err := s.pool.Exec(ctx, q, olderThan.UTC())
	if err != nil {
		return 0, fmt.Errorf("oauthcode: sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RegisterRedirectURI persists a (client_id, redirect_uri) pair.
// Returns ErrDuplicateRedirectURI when the pair is already
// registered.
func (s *Store) RegisterRedirectURI(ctx context.Context, clientID, redirectURI string) error {
	if clientID == "" || redirectURI == "" {
		return errors.New("oauthcode: client_id and redirect_uri are required")
	}
	const q = `
		INSERT INTO oauth_client_redirect_uris (client_id, redirect_uri)
		VALUES ($1, $2)
	`
	if _, err := s.pool.Exec(ctx, q, clientID, redirectURI); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
			return ErrDuplicateRedirectURI
		}
		return fmt.Errorf("oauthcode: register redirect_uri: %w", err)
	}
	return nil
}

// IsRedirectURIRegistered returns true iff (client_id, redirect_uri)
// exists in the registry. This is the open-redirect-prevention gate
// the authorize handler calls on every request.
//
// DEPRECATED: prefer LookupRedirectURI for new call sites — it returns
// the registered URI value from the DB rather than echoing the
// caller-supplied string back, which CodeQL recognises as a sanitizer
// boundary (slice 189 D5 / CodeQL alert #36).
func (s *Store) IsRedirectURIRegistered(ctx context.Context, clientID, redirectURI string) (bool, error) {
	if clientID == "" || redirectURI == "" {
		return false, nil
	}
	const q = `
		SELECT 1 FROM oauth_client_redirect_uris
		WHERE client_id = $1 AND redirect_uri = $2
		LIMIT 1
	`
	var one int
	err := s.pool.QueryRow(ctx, q, clientID, redirectURI).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("oauthcode: redirect_uri lookup: %w", err)
	}
	return true, nil
}

// LookupRedirectURI looks up the registered redirect URI for
// (clientID, requestedURI) and returns the DB-stored URI value on
// match (along with `true`). Returns `("", false, nil)` when the
// pair is unregistered.
//
// The returned `registeredURI` is the value AS STORED in
// `oauth_client_redirect_uris.redirect_uri` — it equals the
// `requestedURI` parameter on a successful match (the WHERE clause
// enforces exact equality), but the data-flow path goes
// (DB row → handler) rather than (URL query → handler), which is
// the taint-safe boundary CodeQL recognises (slice 189 D5 / CodeQL
// alert #36). Use this method's return value for the actual
// `http.Redirect` target.
func (s *Store) LookupRedirectURI(ctx context.Context, clientID, requestedURI string) (string, bool, error) {
	if clientID == "" || requestedURI == "" {
		return "", false, nil
	}
	const q = `
		SELECT redirect_uri FROM oauth_client_redirect_uris
		WHERE client_id = $1 AND redirect_uri = $2
		LIMIT 1
	`
	var registeredURI string
	err := s.pool.QueryRow(ctx, q, clientID, requestedURI).Scan(&registeredURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("oauthcode: redirect_uri lookup: %w", err)
	}
	return registeredURI, true, nil
}
