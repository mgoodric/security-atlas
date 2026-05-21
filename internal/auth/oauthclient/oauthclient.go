// Package oauthclient is the DB-backed registry of OAuth client
// identities the slice-188 token endpoint authenticates against.
//
// A `Client` row in the `oauth_clients` table represents a machine
// identity authorised to call `POST /oauth/token` with
// `grant_type=client_credentials` (RFC 6749 §4.4). The plaintext
// `client_secret` is returned EXACTLY ONCE at issuance — only the
// argon2id-encoded hash is persisted, and verification runs in
// constant time via the `internal/auth/password` package (the same
// argon2id surface used for human password verification).
//
// The store deliberately does NOT extend the `dbx` sqlc package
// because the table has only two production reads (lookup-by-name at
// issuance time, lookup-by-client_id at authentication time) and one
// production write (INSERT at issuance). Hand-written pgx keeps the
// migration footprint of slice 188 small. If a future slice needs
// richer query surface, adding `oauth_clients.sql` to
// `internal/db/queries/` is a mechanical follow-on.
//
// Tenancy: `oauth_clients` is NOT tenant-scoped (see migration header
// comment) — the table holds platform-global identities. The
// connection used by the store does NOT need a tenant GUC set.
// However, the platform's `atlas_app` role does have RLS enforced on
// other tables; this package uses BeginTx WITHOUT calling
// `tenancy.ApplyTenant` because the queries here read/write only the
// (RLS-exempt) `oauth_clients` table.
//
// Anti-criteria honored:
//
//   - P0-188-3: NEVER stores or logs plaintext secrets. The
//     plaintext returned by `Issue` is the only path that ever sees
//     the 32 random bytes; the caller (CLI) prints it once to stdout
//     and discards.
//   - The `Verify` path uses constant-time comparison via
//     password.Verify (subtle.ConstantTimeCompare under the hood).
package oauthclient

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

// Postgres SQLSTATE for unique_violation. Used to map duplicate-name
// inserts to ErrDuplicateName so the CLI can produce a useful
// "duplicate name; choose another" message rather than a generic
// 23505 error.
const pgErrUniqueViolation = "23505"

// SecretByteLen is the entropy of the plaintext client secret. 32
// bytes = 256 bits; brute-force is infeasible. base64 encoding makes
// the secret ~43 characters on the wire.
const SecretByteLen = 32

// ErrDuplicateName is returned by Issue when a client with the same
// name already exists. The CLI translates this to exit code 1 + a
// human message; the HTTP layer never surfaces this error (only
// admin / CLI paths call Issue).
var ErrDuplicateName = errors.New("oauthclient: client with that name already exists")

// ErrUnknownClient is the sentinel for "no oauth_clients row matched
// the supplied client_id" OR "the matched row's client_secret
// disagreed with the supplied plaintext" OR "the matched row is
// disabled". The token handler collapses all three into a 401
// `invalid_client` response (RFC 6749 §5.2) to avoid leaking
// client-existence oracles.
var ErrUnknownClient = errors.New("oauthclient: unknown client")

// Client is the in-memory projection of an oauth_clients row. The
// `client_secret_hash` column is deliberately NOT exposed — the hash
// is an implementation detail of the verification path.
type Client struct {
	ID         uuid.UUID
	ClientID   string
	Name       string
	CreatedAt  time.Time
	DisabledAt *time.Time
}

// Store wraps a pgxpool with the issuance and verification primitives
// the OAuth token endpoint needs.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool can be either the
// tenant-scoped atlas_app pool or a BYPASSRLS pool — oauth_clients
// has no RLS, so either works. Production wiring passes the
// app-tenant pool; tests pass the integration test pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Issue creates a new OAuth client identity. Returns the public
// client_id (the form-param value the caller will present at
// `/oauth/token`), the plaintext client_secret (presented ONCE to the
// operator and never re-readable), and the persisted Client row.
//
// Concurrency: an INSERT that races against another INSERT with the
// same `name` is mapped to ErrDuplicateName by inspecting the
// unique_violation SQLSTATE. The DB UNIQUE constraint on (name) is
// the source of truth.
func (s *Store) Issue(ctx context.Context, name string) (*Client, string, error) {
	if name == "" {
		return nil, "", errors.New("oauthclient: name is empty")
	}
	secret, err := generateSecret()
	if err != nil {
		return nil, "", fmt.Errorf("oauthclient: generate secret: %w", err)
	}
	hash, err := password.Hash(secret)
	if err != nil {
		return nil, "", fmt.Errorf("oauthclient: hash secret: %w", err)
	}

	clientID := uuid.NewString()
	row := &Client{
		ID:       uuid.New(),
		ClientID: clientID,
		Name:     name,
	}

	const q = `
		INSERT INTO oauth_clients (id, client_id, client_secret_hash, name)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at
	`
	if err := s.pool.QueryRow(ctx, q,
		row.ID, row.ClientID, hash, row.Name,
	).Scan(&row.CreatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
			return nil, "", ErrDuplicateName
		}
		return nil, "", fmt.Errorf("oauthclient: insert: %w", err)
	}
	return row, secret, nil
}

// Verify is the hot-path the token endpoint calls on every
// client_credentials request. Looks up the row by client_id,
// constant-time compares the supplied plaintext against the stored
// argon2id hash, and returns the Client on success. Returns
// ErrUnknownClient on any failure (no row / hash mismatch / disabled).
//
// The error collapse is deliberate per RFC 6749 §5.2: a token
// endpoint MUST NOT reveal whether the client_id existed vs the
// secret was wrong — the security boundary is the joint check.
func (s *Store) Verify(ctx context.Context, clientID, secretPlaintext string) (*Client, error) {
	if clientID == "" || secretPlaintext == "" {
		return nil, ErrUnknownClient
	}
	const q = `
		SELECT id, client_id, client_secret_hash, name, disabled_at, created_at
		FROM oauth_clients
		WHERE client_id = $1
	`
	var (
		c        Client
		hash     string
		disabled *time.Time
	)
	err := s.pool.QueryRow(ctx, q, clientID).Scan(
		&c.ID, &c.ClientID, &hash, &c.Name, &disabled, &c.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownClient
		}
		return nil, fmt.Errorf("oauthclient: query: %w", err)
	}
	if disabled != nil {
		// Disabled clients always return the same opaque error as
		// "unknown client" — an operator-revoked client_id MUST NOT
		// be distinguishable from a typo.
		return nil, ErrUnknownClient
	}
	c.DisabledAt = disabled

	// Constant-time compare via the existing argon2id surface.
	ok, err := password.Verify(secretPlaintext, hash)
	if err != nil {
		// password.Verify returns ErrInvalidHash if the stored hash
		// is corrupt; collapse to unknown-client to avoid leaking
		// the corruption to the caller.
		return nil, ErrUnknownClient
	}
	if !ok {
		return nil, ErrUnknownClient
	}
	return &c, nil
}

// generateSecret produces a 32-byte random secret encoded as
// URL-safe base64 (no padding). 32 bytes is 256 bits of entropy —
// brute-force is computationally infeasible. URL-safe encoding lets
// the secret travel cleanly in form bodies and (someday) query
// strings without further escaping.
func generateSecret() (string, error) {
	buf := make([]byte, SecretByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
