// Package users persists local + OIDC-provisioned user identities.
//
// The store wraps the sqlc Queries with the tenancy.ApplyTenant transaction
// pattern shared by the rest of the platform. Local users (no IdP backing)
// carry empty idp_issuer/idp_subject strings; OIDC users carry the IdP's
// canonical pair.
//
// Slice 198 adds BootstrapFirstInstallOrUpsert — the OIDC-first-install
// path that creates the Default Tenant + super_admin grant when the
// `tenants` table is empty. The bootstrap branch is wired to an optional
// BYPASSRLS auth pool because the standard `users` RLS context cannot
// apply when no tenant exists yet.
package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/password"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultBootstrapTenantName is the human-readable name the bootstrap
// branch writes to the first tenant's `name` field. Operators can rename
// post-install via slice 144's PATCH /v1/tenants/{id}.
const DefaultBootstrapTenantName = "Default Tenant"

// BootstrapGrantVia is the value written to `super_admins.granted_via` +
// `me_audit_log.action` for the bootstrap path. Slice 198's CHECK
// constraints admit this exact string; future maintainer-CLI grants
// would add their own values.
const BootstrapGrantVia = "bootstrap_first_install"

// pgUniqueViolation is the Postgres SQLSTATE for `unique_violation`
// (23505). The bootstrap branch handles this on
// `idx_tenants_bootstrap_singleton` collision (concurrent first-install
// race per P0-198-3) — the loser retries the count check and falls
// through to the established-install path.
const pgUniqueViolation = "23505"

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
	// TimeZone is an IANA timezone name (e.g. "America/Los_Angeles") or
	// "" (empty = browser-derived; the backend never invents a timezone).
	// Slice 108 added the column via an additive ALTER on users.
	TimeZone  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store wraps pgx + sqlc with tenancy plumbing.
//
// authPool (optional) is the BYPASSRLS atlas_migrate pool used by the
// slice 198 bootstrap branch — it lets the first-install path write
// rows across tables (tenants, users, user_roles, super_admins,
// me_audit_log) without an established tenant context. When nil, the
// bootstrap branch is disabled (callers receive an error sentinel).
type Store struct {
	pool     *pgxpool.Pool
	authPool *pgxpool.Pool
}

// NewStore constructs a Store with no bootstrap-pool wired. The slice
// 198 bootstrap branch returns ErrBootstrapUnavailable until
// NewStoreWithAuthPool is used (or AttachAuthPool is called).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// NewStoreWithAuthPool constructs a Store with the BYPASSRLS auth pool
// wired for the slice 198 bootstrap branch.
func NewStoreWithAuthPool(pool, authPool *pgxpool.Pool) *Store {
	return &Store{pool: pool, authPool: authPool}
}

// AttachAuthPool wires the BYPASSRLS auth pool after construction.
// Callers who construct via NewStore and later acquire an auth pool
// (e.g., delayed DATABASE_URL resolution at startup) use this to
// enable the bootstrap branch.
func (s *Store) AttachAuthPool(p *pgxpool.Pool) { s.authPool = p }

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

// ErrBootstrapUnavailable is returned by BootstrapFirstInstallOrUpsert
// when the Store was constructed without an auth pool. The handler
// path falls through to the established-install code in that case
// (which itself fails with the existing "tenant_id required" error,
// preserving the pre-slice-198 behavior).
var ErrBootstrapUnavailable = errors.New("users: bootstrap unavailable (no auth pool)")

// BootstrapInput captures what the OIDC callback hands the bootstrap
// branch on first install. Identical shape to UpsertOIDCInput minus the
// TenantID — the bootstrap branch SYNTHESIZES the new tenant ID
// internally.
type BootstrapInput struct {
	Email       string
	DisplayName string
	Issuer      string
	Subject     string
}

// BootstrapResult is what BootstrapFirstInstallOrUpsert returns.
//
//   - Bootstrapped=true means this call landed the first row in tenants
//
//   - created the OIDC user + granted super_admin + tenant_admin +
//     wrote the audit row. The TenantID + UserID fields carry the
//     synthesized IDs. The caller MUST NOT call UpsertOIDC after this;
//     the bootstrap branch already created the row.
//
//   - Bootstrapped=false means the count(*) gate found ≥1 tenant. The
//     caller falls through to the existing UpsertOIDC path. The
//     TenantID and UserID fields are zero-valued.
type BootstrapResult struct {
	Bootstrapped bool
	TenantID     uuid.UUID
	UserID       uuid.UUID
}

// BootstrapFirstInstallOrUpsert is the slice 198 OIDC-first-install
// path. The method runs the count(*) gate, and if zero, atomically:
//
//  1. Creates the Default Tenant row with is_bootstrap_tenant=true.
//     (slice 144 partial UNIQUE index serializes against concurrent
//     first-installers.)
//  2. Creates the OIDC users row under the new tenant.
//  3. Inserts user_roles(role='admin') for the user under the new
//     tenant.
//  4. Inserts super_admins(user_id, granted_via='bootstrap_first_install').
//  5. Inserts me_audit_log(action='bootstrap_first_install').
//  6. Commits.
//
// Race-handling: on SQLSTATE 23505 from the tenants insert (concurrent
// first-installer beat us to the partial UNIQUE index), the
// transaction rolls back, we recheck count(*), and fall through with
// Bootstrapped=false.
//
// When the Store lacks an authPool, returns ErrBootstrapUnavailable —
// the caller's existing UpsertOIDC path is unaffected.
//
// Constitutional invariants honored:
//
//   - P0-198-1: single transaction across all five inserts.
//   - P0-198-2: count(*) gate is the first read.
//   - P0-198-3: race-handling via 23505 retry-then-fall-through.
//   - P0-198-4: me_audit_log row in same tx as the grants.
//   - P0-198-5: super_admins INSERT carries no tenant_id (the table
//     has no tenant_id column by design).
func (s *Store) BootstrapFirstInstallOrUpsert(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	if s.authPool == nil {
		return BootstrapResult{}, ErrBootstrapUnavailable
	}
	if in.Issuer == "" || in.Subject == "" {
		return BootstrapResult{}, fmt.Errorf("users: BootstrapFirstInstallOrUpsert requires non-empty issuer + subject")
	}

	// One retry on concurrent-first-install race. Past one retry, the
	// race has resolved (either we won or someone else did), so the
	// loop terminates deterministically.
	for attempt := 0; attempt < 2; attempt++ {
		res, retry, err := s.bootstrapAttempt(ctx, in)
		if err != nil {
			return BootstrapResult{}, err
		}
		if !retry {
			return res, nil
		}
	}
	// Should be unreachable — second attempt's count(*) check sees
	// the winning row and returns Bootstrapped=false.
	return BootstrapResult{Bootstrapped: false}, nil
}

// bootstrapAttempt runs one count-gate + bootstrap-or-skip pass. The
// retry boolean is true when the partial UNIQUE index trip indicates a
// concurrent first-installer beat us; the caller re-enters the loop
// once.
func (s *Store) bootstrapAttempt(ctx context.Context, in BootstrapInput) (BootstrapResult, bool, error) {
	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// P0-198-2: count gate is the first read inside the tx so the
	// established-install path falls through without writes.
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM tenants`).Scan(&count); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap count tenants: %w", err)
	}
	if count > 0 {
		// Established-install path — caller falls through to UpsertOIDC.
		return BootstrapResult{Bootstrapped: false}, false, nil
	}

	// Synthesize the new tenant + user IDs up front so the audit-log
	// row references them.
	tenantID := uuid.New()
	userID := uuid.New()

	// 1. tenants row with is_bootstrap_tenant=true. The slice 144
	//    partial UNIQUE index `idx_tenants_bootstrap_singleton`
	//    serializes against concurrent first-installers.
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, true)`,
		tenantID, DefaultBootstrapTenantName,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			// P0-198-3: concurrent first-installer beat us. Roll back,
			// caller re-enters loop, second pass sees count > 0 and
			// returns Bootstrapped=false.
			return BootstrapResult{}, true, nil
		}
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap insert tenants: %w", err)
	}

	// 2. users row keyed on (tenant_id, idp_issuer, idp_subject).
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
		 VALUES ($1, $2, $3, $4, 'active', $5, $6)`,
		userID, tenantID, in.Email, in.DisplayName, in.Issuer, in.Subject,
	); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap insert user: %w", err)
	}

	// 3. user_roles grant — role='admin' (the slice 035 enum's
	//    per-tenant-admin value). user_roles.user_id is TEXT (not
	//    UUID) per the slice 018 schema; we pass the UUID's String()
	//    form.
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'system:bootstrap_first_install')`,
		tenantID, userID.String(),
	); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap insert user_roles: %w", err)
	}

	// 4. super_admins row — platform-global grant. No tenant_id
	//    column on this table (P0-198-5).
	if _, err := tx.Exec(ctx,
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, $2)`,
		userID, BootstrapGrantVia,
	); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap insert super_admins: %w", err)
	}

	// 5. me_audit_log row — P0-198-4 forensic anchor. before='{}',
	//    after captures the grant snapshot.
	auditAfter := fmt.Sprintf(
		`{"role":"admin","super_admin":true,"granted_via":"%s","idp_issuer":"%s","idp_subject":"%s"}`,
		BootstrapGrantVia, in.Issuer, in.Subject,
	)
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, before, after)
		 VALUES ($1, $2, $3, '{}'::jsonb, $4::jsonb)`,
		tenantID, userID, BootstrapGrantVia, auditAfter,
	); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap insert me_audit_log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap commit: %w", err)
	}

	return BootstrapResult{
		Bootstrapped: true,
		TenantID:     tenantID,
		UserID:       userID,
	}, false, nil
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

// UpdateProfileInput captures the mutable subset of a user profile. Email and
// idp_subject are read-only (managed by the IdP).
type UpdateProfileInput struct {
	TenantID    uuid.UUID
	ID          uuid.UUID
	DisplayName string
	TimeZone    string
}

// UpdateProfile writes the slice 108 PATCH /v1/me mutation. Returns the resulting
// User row. The caller (HTTP handler) computes the diff and decides whether to write
// an audit-log entry; this method is the storage primitive only.
func (s *Store) UpdateProfile(ctx context.Context, in UpdateProfileInput) (User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return User{}, err
	}
	q := dbx.New(tx)
	row, err := q.UpdateUserProfile(ctx, dbx.UpdateUserProfileParams{
		TenantID:    pgtype.UUID{Bytes: in.TenantID, Valid: true},
		ID:          pgtype.UUID{Bytes: in.ID, Valid: true},
		DisplayName: in.DisplayName,
		TimeZone:    in.TimeZone,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("users: update profile: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
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
		TimeZone:    row.TimeZone,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
}
