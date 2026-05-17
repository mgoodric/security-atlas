// Package apikeystore is the DB-backed bearer-credential store.
//
// It persists api_keys rows (slice 034) and authenticates inbound bearer
// tokens by HMAC-SHA256 hash lookup. Plaintext bearer tokens are returned
// exactly once at Issue or Rotate and never persisted.
//
// The store mirrors the surface of internal/api/credstore (the slice-014/018/011
// in-memory store) so HTTP/gRPC middleware can stack the two: DB-backed keys
// authenticate via this store, bootstrap keys via the in-memory one.
//
// Authentication path: Authenticate uses a dedicated pool — `authPool` — that
// is wired with a BYPASSRLS role at platform startup so lookup-by-hash works
// without a tenant context (the request hasn't yet resolved its tenant — the
// row's tenant_id is what authentication RETURNS). All other paths run under
// the tenant-RLS pool with `tenancy.ApplyTenant`.
//
// See docs/adr/0002-bearer-token-storage.md for the hashing rationale.
package apikeystore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrUnknownKey is the sentinel for "no such key, revoked, retired, or expired."
// Mirrors credstore.ErrUnknownKey so the HTTP/gRPC layer can collapse both into
// the same 401 path.
var ErrUnknownKey = errors.New("apikeystore: unknown key")

// Store persists api_keys via sqlc + pgx, hashing bearer tokens with the
// supplied bearer.Hasher (HMAC-SHA256 keyed with BEARER_HASH_KEY).
type Store struct {
	pool          *pgxpool.Pool
	authPool      *pgxpool.Pool
	hasher        *bearer.Hasher
	rotationGrace time.Duration
	prefix        string
}

// NewStore constructs a Store. `pool` is the RLS-enforced application pool
// (atlas_app), used for issue/rotate/revoke/list. `authPool` is a BYPASSRLS
// pool used ONLY for the single lookup-by-hash in Authenticate (necessary
// because the request hasn't resolved its tenant yet). If authPool is nil
// it falls back to pool — fine for tests that pre-set the tenant context.
// `hasher` carries the server's BEARER_HASH_KEY. rotationGrace defaults to
// 7 days when zero.
func NewStore(pool, authPool *pgxpool.Pool, hasher *bearer.Hasher, rotationGrace time.Duration) *Store {
	if rotationGrace == 0 {
		rotationGrace = 7 * 24 * time.Hour
	}
	if authPool == nil {
		authPool = pool
	}
	return &Store{
		pool:          pool,
		authPool:      authPool,
		hasher:        hasher,
		rotationGrace: rotationGrace,
		prefix:        bearer.PrefixProd,
	}
}

// SetPrefix overrides the bearer prefix (defaults to bearer.PrefixProd).
// Tests set bearer.PrefixTest to keep generated tokens out of secret-scanner
// flag patterns.
func (s *Store) SetPrefix(p string) { s.prefix = p }

// IssueInput captures the optional knobs at Issue time. Zero values are valid:
// no scope predicate, no kind restriction, no TTL, no admin/approver flag.
type IssueInput struct {
	ScopePredicate string
	AllowedKinds   []string
	TTL            time.Duration
	IsAdmin        bool
	IsApprover     bool
	OwnerRoles     []string
	IssuedBy       *uuid.UUID
}

// Issue creates a new api_keys row and returns the bearer plaintext exactly
// once. The plaintext is hashed before persistence — nothing in the DB can
// recover it.
func (s *Store) Issue(ctx context.Context, tenantID string, in IssueInput) (credstore.Credential, string, error) {
	plain, err := bearer.Generate(s.prefix)
	if err != nil {
		return credstore.Credential{}, "", err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return credstore.Credential{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return credstore.Credential{}, "", err
	}
	q := dbx.New(tx)
	row, err := s.insert(ctx, q, tenantID, plain, in, uuid.Nil)
	if err != nil {
		return credstore.Credential{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return credstore.Credential{}, "", err
	}
	return credentialFromRow(row), plain, nil
}

// Rotate mints a successor with the same scope/kinds/flags as the predecessor
// and sets the predecessor's `retires_at` to now()+rotationGrace.
func (s *Store) Rotate(ctx context.Context, tenantID string, predecessorID uuid.UUID) (credstore.Credential, string, time.Time, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}
	q := dbx.New(tx)

	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}
	pred, err := q.GetAPIKeyByID(ctx, dbx.GetAPIKeyByIDParams{TenantID: tIDU, ID: pgtype.UUID{Bytes: predecessorID, Valid: true}})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credstore.Credential{}, "", time.Time{}, ErrUnknownKey
		}
		return credstore.Credential{}, "", time.Time{}, err
	}
	if pred.RevokedAt.Valid || pred.RetiresAt.Valid {
		return credstore.Credential{}, "", time.Time{}, ErrUnknownKey
	}

	in := IssueInput{
		ScopePredicate: string(pred.ScopePredicate),
		AllowedKinds:   pred.AllowedKinds,
		TTL:            time.Duration(pred.TtlSeconds) * time.Second,
		IsAdmin:        pred.IsAdmin,
		IsApprover:     pred.IsApprover,
		OwnerRoles:     pred.OwnerRoles,
	}
	predUU, _ := uuidFromPg(pred.ID)
	plain, err := bearer.Generate(s.prefix)
	if err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}
	row, err := s.insert(ctx, q, tenantID, plain, in, predUU)
	if err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}

	retiresAt := time.Now().UTC().Add(s.rotationGrace)
	if err := q.SetAPIKeyRetiresAt(ctx, dbx.SetAPIKeyRetiresAtParams{
		TenantID:  tIDU,
		ID:        pred.ID,
		RetiresAt: pgtype.Timestamptz{Time: retiresAt, Valid: true},
	}); err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return credstore.Credential{}, "", time.Time{}, err
	}
	return credentialFromRow(row), plain, retiresAt, nil
}

// Revoke flips revoked_at on the key.
func (s *Store) Revoke(ctx context.Context, tenantID string, id uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return err
	}
	if _, err := q.GetAPIKeyByID(ctx, dbx.GetAPIKeyByIDParams{TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true}}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownKey
		}
		return err
	}
	if err := q.RevokeAPIKey(ctx, dbx.RevokeAPIKeyParams{TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// List returns active credentials for the tenant in context. Never includes
// bearer plaintexts.
func (s *Store) List(ctx context.Context, tenantID string) ([]credstore.Credential, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListAPIKeysByTenant(ctx, tIDU)
	if err != nil {
		return nil, err
	}
	out := make([]credstore.Credential, 0, len(rows))
	for _, r := range rows {
		out = append(out, credentialFromRow(r))
	}
	return out, nil
}

// Authenticate resolves a plaintext bearer to its credential. Returns
// ErrUnknownKey on no-match / revoked / retired-past-grace / expired.
// On success, last_used_at is bumped best-effort (failure logged, never
// blocks the request).
func (s *Store) Authenticate(ctx context.Context, token string) (credstore.Credential, error) {
	if len(token) == 0 {
		return credstore.Credential{}, ErrUnknownKey
	}
	hash := s.hasher.Hash(token)
	q := dbx.New(s.authPool)
	row, err := q.GetAPIKeyByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credstore.Credential{}, ErrUnknownKey
		}
		return credstore.Credential{}, err
	}
	now := time.Now().UTC()
	if row.RevokedAt.Valid {
		return credstore.Credential{}, ErrUnknownKey
	}
	if row.RetiresAt.Valid && now.After(row.RetiresAt.Time) {
		return credstore.Credential{}, ErrUnknownKey
	}
	if row.ExpiresAt.Valid && now.After(row.ExpiresAt.Time) {
		return credstore.Credential{}, ErrUnknownKey
	}
	// Best-effort last_used_at bump; never blocks.
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = q.TouchAPIKeyLastUsed(bctx, hash)
	}()
	return credentialFromRow(row), nil
}

// --- helpers ---

func (s *Store) insert(ctx context.Context, q *dbx.Queries, tenantID, plain string, in IssueInput, rotatedFrom uuid.UUID) (dbx.ApiKey, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return dbx.ApiKey{}, err
	}
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	tokenHash := s.hasher.Hash(plain)
	scope := []byte(in.ScopePredicate)
	if len(scope) == 0 {
		scope = []byte("{}")
	}
	kinds := in.AllowedKinds
	if kinds == nil {
		kinds = []string{}
	}
	roles := in.OwnerRoles
	if roles == nil {
		roles = []string{}
	}
	var expiresAt pgtype.Timestamptz
	if in.TTL > 0 {
		expiresAt = pgtype.Timestamptz{Time: time.Now().UTC().Add(in.TTL), Valid: true}
	}
	var rotPg pgtype.UUID
	if rotatedFrom != uuid.Nil {
		rotPg = pgtype.UUID{Bytes: rotatedFrom, Valid: true}
	}
	var issuedBy pgtype.UUID
	if in.IssuedBy != nil {
		issuedBy = pgtype.UUID{Bytes: *in.IssuedBy, Valid: true}
	}
	row, err := q.InsertAPIKey(ctx, dbx.InsertAPIKeyParams{
		ID:             id,
		TenantID:       tIDU,
		TokenHash:      tokenHash,
		ScopePredicate: scope,
		AllowedKinds:   kinds,
		IssuedBy:       issuedBy,
		ExpiresAt:      expiresAt,
		RotatedFrom:    rotPg,
		IsAdmin:        in.IsAdmin,
		IsApprover:     in.IsApprover,
		OwnerRoles:     roles,
		Last4:          bearer.Last4(plain),
		TtlSeconds:     int64(in.TTL.Seconds()),
	})
	return row, err
}

func credentialFromRow(row dbx.ApiKey) credstore.Credential {
	idUU, _ := uuidFromPg(row.ID)
	tenUU, _ := uuidFromPg(row.TenantID)
	credID := "key_" + idUU.String()
	// Slice 108: when api_keys.issued_by is set (slice 034 OIDC flow set it from
	// the IdP-provisioned users.id), thread it through as cred.UserID so /v1/me
	// can resolve to a real users row. When unset (bootstrap admin keys, where
	// no users row exists), fall back to the credential id — handlers that need a
	// real users.id check the parse and degrade gracefully.
	userID := credID
	if row.IssuedBy.Valid {
		if u, err := uuidFromPg(row.IssuedBy); err == nil {
			userID = u.String()
		}
	}
	c := credstore.Credential{
		ID:             credID,
		TenantID:       tenUU.String(),
		ScopePredicate: string(row.ScopePredicate),
		Kinds:          append([]string(nil), row.AllowedKinds...),
		TTL:            time.Duration(row.TtlSeconds) * time.Second,
		IssuedAt:       row.IssuedAt.Time,
		Last4:          row.Last4,
		IsAdmin:        row.IsAdmin,
		IsApprover:     row.IsApprover,
		UserID:         userID,
		OwnerRoles:     append([]string(nil), row.OwnerRoles...),
	}
	if row.LastUsedAt.Valid {
		c.LastUsedAt = row.LastUsedAt.Time
	}
	if row.RotatedFrom.Valid {
		ruu, _ := uuidFromPg(row.RotatedFrom)
		c.RotatedFrom = "key_" + ruu.String()
	}
	return c
}

func uuidToPg(s string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("apikeystore: invalid uuid %q: %w", s, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func uuidFromPg(p pgtype.UUID) (uuid.UUID, error) {
	if !p.Valid {
		return uuid.Nil, errors.New("apikeystore: null uuid")
	}
	return uuid.UUID(p.Bytes), nil
}
