package scim

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrUnknownCredential is the sentinel for "no such SCIM credential, or it has
// been revoked." The auth middleware collapses this into a 401.
var ErrUnknownCredential = errors.New("scim: unknown or revoked credential")

// Credential is the metadata view of a SCIM credential. Never includes the
// bearer plaintext. Distinct from credstore.Credential by design (P0-508-2):
// a SCIM credential carries NO IsAdmin / roles / scope-predicate — it can do
// exactly one thing (provision users for its tenant) and nothing else.
type Credential struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Description string
	Last4       string
	IssuedAt    time.Time
	LastUsedAt  time.Time
}

// CredentialStore is the DB-backed SCIM bearer-credential store. It mirrors
// internal/auth/apikeystore: tokens are HMAC-SHA256 hashed at rest; the
// plaintext is returned exactly once at Issue.
//
// pool is the RLS-bound atlas_app pool (issue/list/revoke under a tenant GUC).
// authPool is the BYPASSRLS atlas_migrate pool used ONLY for Authenticate's
// lookup-by-hash (the request has not resolved its tenant yet — the row's
// tenant_id is what authentication RETURNS). When authPool is nil it falls
// back to pool, which is fine for tests that pre-set the tenant context.
type CredentialStore struct {
	pool     *pgxpool.Pool
	authPool *pgxpool.Pool
	hasher   *bearer.Hasher
	prefix   string
}

// NewCredentialStore constructs a CredentialStore.
func NewCredentialStore(pool, authPool *pgxpool.Pool, hasher *bearer.Hasher) *CredentialStore {
	if authPool == nil {
		authPool = pool
	}
	return &CredentialStore{
		pool:     pool,
		authPool: authPool,
		hasher:   hasher,
		prefix:   bearer.PrefixProd,
	}
}

// SetPrefix overrides the bearer prefix (defaults to bearer.PrefixProd).
// Tests set bearer.PrefixTest to keep generated tokens out of secret-scanner
// flag patterns.
func (s *CredentialStore) SetPrefix(p string) { s.prefix = p }

// Issue creates a new scim_credentials row and returns the bearer plaintext
// exactly once. issuedBy is the admin user that minted it (nil for bootstrap).
func (s *CredentialStore) Issue(ctx context.Context, tenantID string, description string, issuedBy *uuid.UUID) (Credential, string, error) {
	plain, err := bearer.Generate(s.prefix)
	if err != nil {
		return Credential{}, "", err
	}
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return Credential{}, "", err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Credential{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Credential{}, "", err
	}
	q := dbx.New(tx)
	var issuedByPg pgtype.UUID
	if issuedBy != nil {
		issuedByPg = pgtype.UUID{Bytes: *issuedBy, Valid: true}
	}
	row, err := q.InsertSCIMCredential(ctx, dbx.InsertSCIMCredentialParams{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		TenantID:    tIDU,
		TokenHash:   s.hasher.Hash(plain),
		Description: description,
		IssuedBy:    issuedByPg,
		Last4:       bearer.Last4(plain),
	})
	if err != nil {
		return Credential{}, "", fmt.Errorf("scim: insert credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, "", err
	}
	return credentialFromRow(row), plain, nil
}

// Authenticate resolves a plaintext bearer to its SCIM credential. Returns
// ErrUnknownCredential on no-match or revoked. On success last_used_at is
// bumped best-effort (failure logged by the caller, never blocks the request).
//
// This is THE scope boundary (P0-508-2): the SCIM auth middleware calls ONLY
// this method. There is no path from a SCIM token to a credstore.Credential,
// an atlas JWT, or a human session.
func (s *CredentialStore) Authenticate(ctx context.Context, token string) (Credential, error) {
	if token == "" {
		return Credential{}, ErrUnknownCredential
	}
	hash := s.hasher.Hash(token)
	q := dbx.New(s.authPool)
	row, err := q.GetSCIMCredentialByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Credential{}, ErrUnknownCredential
		}
		return Credential{}, err
	}
	if row.RevokedAt.Valid {
		return Credential{}, ErrUnknownCredential
	}
	// Best-effort last_used_at bump; never blocks.
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = q.TouchSCIMCredentialLastUsed(bctx, hash)
	}()
	return credentialFromRow(row), nil
}

// Revoke flips revoked_at on the credential (AC-3). Idempotent at the SQL
// layer (the UPDATE matches by tenant+id; a missing row is reported as
// ErrUnknownCredential via the prior GET).
func (s *CredentialStore) Revoke(ctx context.Context, tenantID string, id uuid.UUID) error {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return err
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
	if _, err := q.GetSCIMCredentialByID(ctx, dbx.GetSCIMCredentialByIDParams{
		TenantID: tIDU,
		ID:       pgtype.UUID{Bytes: id, Valid: true},
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownCredential
		}
		return err
	}
	if err := q.RevokeSCIMCredential(ctx, dbx.RevokeSCIMCredentialParams{
		TenantID: tIDU,
		ID:       pgtype.UUID{Bytes: id, Valid: true},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// List returns active SCIM credentials for the tenant in context. Never
// includes bearer plaintexts.
func (s *CredentialStore) List(ctx context.Context, tenantID string) ([]Credential, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	rows, err := q.ListSCIMCredentialsByTenant(ctx, tIDU)
	if err != nil {
		return nil, err
	}
	out := make([]Credential, 0, len(rows))
	for _, r := range rows {
		out = append(out, credentialFromRow(r))
	}
	return out, nil
}

func credentialFromRow(row dbx.ScimCredential) Credential {
	c := Credential{
		ID:          uuid.UUID(row.ID.Bytes),
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		Description: row.Description,
		Last4:       row.Last4,
	}
	if row.IssuedAt.Valid {
		c.IssuedAt = row.IssuedAt.Time
	}
	if row.LastUsedAt.Valid {
		c.LastUsedAt = row.LastUsedAt.Time
	}
	return c
}

func uuidToPg(s string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("scim: invalid uuid %q: %w", s, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}
