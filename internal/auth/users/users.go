// Package users persists local + OIDC-provisioned user identities.
//
// The store wraps the sqlc Queries with the tenancy.ApplyTenant transaction
// pattern shared by the rest of the platform. Local users (no IdP backing)
// carry empty idp_issuer/idp_subject strings; OIDC users carry the IdP's
// canonical pair.
package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/password"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is the sentinel for "no such user under this tenant."
var ErrNotFound = errors.New("users: not found")

// ErrInvalidCredentials is the sentinel for either "no local_credentials row"
// or "password mismatch." Login handlers collapse both into 401 to avoid
// account-existence oracles.
var ErrInvalidCredentials = errors.New("users: invalid credentials")

// User is the domain projection of a row.
type User struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Email       string
	DisplayName string
	Status      string
	IdpIssuer   string
	IdpSubject  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store wraps pgx + sqlc with tenancy plumbing.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateLocalInput captures the fields for /v1/admin/users (local-mode
// provisioning). The password is hashed inside the store; plaintext never
// leaves this call.
type CreateLocalInput struct {
	TenantID    uuid.UUID
	Email       string
	DisplayName string
	Password    string
}

// CreateLocal provisions a local-mode user + its argon2id password hash in
// one transaction. The two rows live or die together.
func (s *Store) CreateLocal(ctx context.Context, in CreateLocalInput) (User, error) {
	hash, err := password.Hash(in.Password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return User{}, err
	}
	q := dbx.New(tx)

	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	tIDU := pgtype.UUID{Bytes: in.TenantID, Valid: true}
	row, err := q.CreateUser(ctx, dbx.CreateUserParams{
		ID:          id,
		TenantID:    tIDU,
		Email:       in.Email,
		DisplayName: in.DisplayName,
		Status:      "active",
	})
	if err != nil {
		return User{}, fmt.Errorf("users: create: %w", err)
	}
	if err := q.UpsertLocalCredential(ctx, dbx.UpsertLocalCredentialParams{
		UserID:       row.ID,
		TenantID:     tIDU,
		PasswordHash: hash,
		Algo:         password.Algorithm,
		Params:       []byte("{}"),
	}); err != nil {
		return User{}, fmt.Errorf("users: upsert credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return userFromRow(row), nil
}

// UpsertOIDCInput is what the OIDC callback hands the store after a
// successful code exchange + ID-token verification.
type UpsertOIDCInput struct {
	TenantID    uuid.UUID
	Email       string
	DisplayName string
	Issuer      string
	Subject     string
}

// UpsertOIDC provisions-or-updates the user keyed on (idp_issuer, idp_subject).
// Returns the resulting row.
func (s *Store) UpsertOIDC(ctx context.Context, in UpsertOIDCInput) (User, error) {
	if in.Issuer == "" || in.Subject == "" {
		return User{}, fmt.Errorf("users: UpsertOIDC requires non-empty issuer + subject")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return User{}, err
	}
	q := dbx.New(tx)

	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	tIDU := pgtype.UUID{Bytes: in.TenantID, Valid: true}
	row, err := q.UpsertUserByIdpSubject(ctx, dbx.UpsertUserByIdpSubjectParams{
		ID:          id,
		TenantID:    tIDU,
		Email:       in.Email,
		DisplayName: in.DisplayName,
		Status:      "active",
		IdpIssuer:   in.Issuer,
		IdpSubject:  in.Subject,
	})
	if err != nil {
		return User{}, fmt.Errorf("users: upsert OIDC: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return userFromRow(row), nil
}

// VerifyLocalLogin returns the User on a successful (tenant, email, password)
// triple. Returns ErrInvalidCredentials when either no user exists or the
// password does not verify — never both. The caller (HTTP login handler)
// surfaces 401 on this sentinel.
func (s *Store) VerifyLocalLogin(ctx context.Context, tenantID uuid.UUID, email, plaintextPassword string) (User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return User{}, err
	}
	q := dbx.New(tx)

	tIDU := pgtype.UUID{Bytes: tenantID, Valid: true}
	row, err := q.GetUserByEmail(ctx, dbx.GetUserByEmailParams{TenantID: tIDU, Email: email})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	cred, err := q.GetLocalCredentialByUserID(ctx, dbx.GetLocalCredentialByUserIDParams{TenantID: tIDU, UserID: row.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	ok, err := password.Verify(plaintextPassword, cred.PasswordHash)
	if err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	return userFromRow(row), nil
}

// GetByID returns the user under (tenant, id) or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return User{}, err
	}
	q := dbx.New(tx)
	row, err := q.GetUserByID(ctx, dbx.GetUserByIDParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		ID:       pgtype.UUID{Bytes: id, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return userFromRow(row), nil
}

func userFromRow(row dbx.User) User {
	return User{
		ID:          uuid.UUID(row.ID.Bytes),
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		Email:       row.Email,
		DisplayName: row.DisplayName,
		Status:      row.Status,
		IdpIssuer:   row.IdpIssuer,
		IdpSubject:  row.IdpSubject,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
}
