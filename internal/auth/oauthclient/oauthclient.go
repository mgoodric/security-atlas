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
	"log/slog"
	"sync"
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

// ErrAlreadyBound is returned by Bind when the client is already bound to
// a different tenant. Moving a client between tenants would silently
// redirect every write its tokens make, so it is refused rather than done.
var ErrAlreadyBound = errors.New("oauthclient: client is bound to a different tenant")

// ErrNilTenant is returned when a caller asks to bind a client to the
// all-zeros tenant, which is not a tenant anyone means.
var ErrNilTenant = errors.New("oauthclient: refusing to bind to the all-zeros tenant")

// Client is the in-memory projection of an oauth_clients row. The
// `client_secret_hash` column is deliberately NOT exposed — the hash
// is an implementation detail of the verification path.
type Client struct {
	ID         uuid.UUID
	ClientID   string
	Name       string
	CreatedAt  time.Time
	DisabledAt *time.Time
	// TenantID is the tenant this client's client_credentials tokens act
	// in (security-atlas#1739). Nil means unbound: its tokens carry no
	// tenant and every write they attempt is refused.
	TenantID *uuid.UUID
}

// Store wraps a pgxpool with the issuance and verification primitives
// the OAuth token endpoint needs.
type Store struct {
	pool *pgxpool.Pool
	// verifier and dummyHash are seams for the timing-equalizer unit
	// test; New wires password.Verify and timingDummyHash.
	verifier  secretVerifier
	dummyHash func() string
}

// secretVerifier checks a plaintext secret against an encoded argon2id
// hash. Production uses password.Verify; the unit test counts calls.
type secretVerifier interface {
	Verify(plaintext, encoded string) (bool, error)
}

type passwordVerifier struct{}

func (passwordVerifier) Verify(plaintext, encoded string) (bool, error) {
	return password.Verify(plaintext, encoded)
}

// timingDummyHash is a client-secret hash no presented secret is expected
// to match. Verify checks the presented secret against it when the
// client_id is unknown, empty or disabled, so every rejection costs the
// same argon2id work as a wrong secret and response time on /oauth/token,
// /oauth/introspect and /oauth/revoke is no client_id oracle
// (security-atlas#2241). It is built with password.Hash, the function
// that hashes real client secrets, so the two share argon2id parameters.
//
// A failed build is logged and returns "": Verify then fails every call
// closed rather than answering a fast rejection.
var timingDummyHash = sync.OnceValue(func() string {
	h, err := password.Hash("atlas-oauth-client-timing-equalizer")
	if err != nil {
		slog.Error("oauthclient: build timing-equalizer hash; client authentication fails closed", "err", err)
		return ""
	}
	return h
})

// errTimingEqualizerUnavailable fails every Verify closed when the dummy
// hash could not be built: rejecting without the argon2id work would make
// response time a client_id oracle.
var errTimingEqualizerUnavailable = errors.New("oauthclient: timing equalizer unavailable")

// New returns a Store backed by pool. The pool can be either the
// tenant-scoped atlas_app pool or a BYPASSRLS pool — oauth_clients
// has no RLS, so either works. Production wiring passes the
// app-tenant pool; tests pass the integration test pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, verifier: passwordVerifier{}, dummyHash: timingDummyHash}
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
	return s.issue(ctx, name, nil)
}

// IssueForTenant is Issue for a client bound to tenant at creation, so its
// client_credentials tokens act in that tenant (security-atlas#1739).
func (s *Store) IssueForTenant(ctx context.Context, name string, tenant uuid.UUID) (*Client, string, error) {
	if tenant == uuid.Nil {
		return nil, "", ErrNilTenant
	}
	return s.issue(ctx, name, &tenant)
}

func (s *Store) issue(ctx context.Context, name string, tenant *uuid.UUID) (*Client, string, error) {
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
		TenantID: tenant,
	}

	const q = `
		INSERT INTO oauth_clients (id, client_id, client_secret_hash, name, bound_tenant_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at
	`
	if err := s.pool.QueryRow(ctx, q,
		row.ID, row.ClientID, hash, row.Name, row.TenantID,
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
//
// Every rejection costs one argon2id verification, so response time does
// not tell an unknown client_id from a known one (security-atlas#2241).
// Without the timing-equalizer hash every call fails closed before the
// lookup.
func (s *Store) Verify(ctx context.Context, clientID, secretPlaintext string) (*Client, error) {
	dummyHash := s.dummyHash()
	if dummyHash == "" {
		return nil, errTimingEqualizerUnavailable
	}
	var (
		row  *Client
		hash string
	)
	if clientID != "" {
		var err error
		row, hash, err = s.lookupForVerify(ctx, clientID)
		if err != nil {
			return nil, err
		}
	}
	return s.authenticate(row, hash, secretPlaintext, dummyHash)
}

// lookupForVerify reads the client row and its secret hash. A missing row
// is (nil, "", nil), not an error: authenticate owns every rejection so
// each one does the same argon2id work.
func (s *Store) lookupForVerify(ctx context.Context, clientID string) (*Client, string, error) {
	const q = `
		SELECT id, client_id, client_secret_hash, name, disabled_at, created_at, bound_tenant_id
		FROM oauth_clients
		WHERE client_id = $1
	`
	var (
		c    Client
		hash string
	)
	err := s.pool.QueryRow(ctx, q, clientID).Scan(
		&c.ID, &c.ClientID, &hash, &c.Name, &c.DisabledAt, &c.CreatedAt, &c.TenantID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("oauthclient: query: %w", err)
	}
	return &c, hash, nil
}

// authenticate decides a Verify call once the row is read. row is nil when
// no client matched. Every path runs one argon2id verification: an
// unknown, empty or disabled (revoked) client_id is checked against
// dummyHash in place of a real hash.
func (s *Store) authenticate(row *Client, hash, secretPlaintext, dummyHash string) (*Client, error) {
	usable := row != nil && row.DisabledAt == nil
	if !usable {
		// Unknown and disabled clients return the same opaque error as
		// a wrong secret: an operator-revoked client_id MUST NOT be
		// distinguishable from a typo, by error or by timing.
		hash = dummyHash
	}
	ok, err := s.verifier.Verify(secretPlaintext, hash)
	if err != nil {
		// A corrupt stored hash fails decode before any argon2id work.
		// Burn the dummy verify so the rejection still costs the same,
		// and collapse to unknown-client so the corruption does not
		// leak to the caller.
		_, _ = s.verifier.Verify(secretPlaintext, dummyHash)
		return nil, ErrUnknownClient
	}
	if !ok || !usable || secretPlaintext == "" {
		return nil, ErrUnknownClient
	}
	return row, nil
}

// Lookup is the secret-less read path the slice-191 device
// authorization flow uses to validate that a client_id is
// registered. Returns ErrUnknownClient when no row matches or when
// the matched row is disabled — the device flow's security model
// is that the JWT mint is gated by the human approval step
// (RFC 8628 §5.1), not by holding a static client_secret, so this
// path deliberately does NOT verify a secret. It is NOT a
// substitute for Verify on the client_credentials hot path.
func (s *Store) Lookup(ctx context.Context, clientID string) (*Client, error) {
	if clientID == "" {
		return nil, ErrUnknownClient
	}
	const q = `
		SELECT id, client_id, name, disabled_at, created_at, bound_tenant_id
		FROM oauth_clients
		WHERE client_id = $1
	`
	var (
		c        Client
		disabled *time.Time
	)
	err := s.pool.QueryRow(ctx, q, clientID).Scan(
		&c.ID, &c.ClientID, &c.Name, &disabled, &c.CreatedAt, &c.TenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownClient
		}
		return nil, fmt.Errorf("oauthclient: query: %w", err)
	}
	if disabled != nil {
		return nil, ErrUnknownClient
	}
	c.DisabledAt = disabled
	return &c, nil
}

// Bind binds the client identified by clientID (the public client_id) to
// tenant, so its client_credentials tokens act in that tenant
// (security-atlas#1739). Binding an unbound client, or re-binding to the
// tenant it already has, succeeds; a client bound to a different tenant
// returns ErrAlreadyBound. A disabled or missing client returns
// ErrUnknownClient. The tenant must exist (oauth_clients.bound_tenant_id is a
// foreign key to tenants).
func (s *Store) Bind(ctx context.Context, clientID string, tenant uuid.UUID) (*Client, error) {
	if tenant == uuid.Nil {
		return nil, ErrNilTenant
	}
	if clientID == "" {
		return nil, ErrUnknownClient
	}
	const q = `
		UPDATE oauth_clients
		SET bound_tenant_id = $2
		WHERE client_id = $1
		  AND disabled_at IS NULL
		  AND (bound_tenant_id IS NULL OR bound_tenant_id = $2)
		RETURNING id, client_id, name, created_at, bound_tenant_id
	`
	var c Client
	err := s.pool.QueryRow(ctx, q, clientID, tenant).Scan(
		&c.ID, &c.ClientID, &c.Name, &c.CreatedAt, &c.TenantID,
	)
	if err == nil {
		return &c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("oauthclient: bind: %w", err)
	}
	// No row updated: the client is missing, disabled, or bound to
	// another tenant. Lookup tells the first two from the third.
	if _, lerr := s.Lookup(ctx, clientID); lerr != nil {
		return nil, lerr
	}
	return nil, ErrAlreadyBound
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
