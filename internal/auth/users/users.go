// Package users persists local + OIDC-provisioned user identities.
//
// The store wraps the sqlc Queries with the tenancy.ApplyTenant transaction
// pattern shared by the rest of the platform. Local users (no IdP backing)
// carry empty idp_issuer/idp_subject strings; OIDC users carry the IdP's
// canonical pair.
//
// Slice 198 adds BootstrapFirstInstallOrUpsert — the OIDC-first-install
// path that attaches the first user to the bootstrap tenant (creating it
// only when no tenant exists) with a super_admin grant. The bootstrap
// branch is wired to an optional
// BYPASSRLS auth pool because the standard `users` RLS context cannot
// apply when no tenant exists yet.
package users

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// bootstrapAdvisoryLockKey is the fixed pg_advisory_xact_lock(BIGINT) key
// that serializes OIDC first-installers (#1759). Fixed and documented, like
// slice 142's 0x142142142142 super-admin lock: 0x198 names slice 198, which
// introduced this bootstrap path. Do not reuse it for another lock.
const bootstrapAdvisoryLockKey int64 = 0x198198198198

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
	// Active is the SCIM deprovision mirror (users.active); false once the
	// IdP deprovisions the user.
	Active     bool
	IdpIssuer  string
	IdpSubject string
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
//   - Bootstrapped=true means this call created the first user, in the
//     bootstrap tenant, granted super_admin + tenant_admin and wrote the
//     audit row. TenantID is the bootstrap tenant; UserID is the new
//     user. The caller MUST NOT call UpsertOIDC after this; the
//     bootstrap branch already created the row.
//
//   - Bootstrapped=false means a user or a super_admin already exists. The caller falls
//     through to the existing UpsertOIDC path. The TenantID and UserID
//     fields are zero-valued.
type BootstrapResult struct {
	Bootstrapped bool
	TenantID     uuid.UUID
	UserID       uuid.UUID
}

// BootstrapFirstInstallOrUpsert is the slice 198 OIDC-first-install
// path. Under a transaction-scoped advisory lock (READ COMMITTED) it checks
// whether any user or super_admin exists, and if none does, atomically:
//
//  1. Resolves the implied tenant (tenancy.ResolveImplicitTenant). The
//     bootstrap tenant is canonical: with one tenant (e.g. the one
//     bootstrap.sh seeded) the user attaches to it, flagging it
//     is_bootstrap_tenant if no tenant is flagged yet; with none it
//     creates the Default Tenant row with is_bootstrap_tenant=true. With
//     several it returns an error wrapping *tenancy.AmbiguousTenantError.
//  2. Creates the OIDC users row under that tenant.
//  3. Inserts user_roles(role='admin') for the user under that tenant.
//  4. Inserts super_admins(user_id, granted_via='bootstrap_first_install').
//  5. Inserts me_audit_log(action='bootstrap_first_install').
//  6. Commits.
//
// Race-handling: the advisory lock serializes first-installers; the
// loser sees the winner's user and falls through. On SQLSTATE 23505
// from the tenants insert (partial UNIQUE index, second leg) the
// transaction rolls back and the gate is re-run once.
//
// When the Store lacks an authPool, returns ErrBootstrapUnavailable —
// the caller's existing UpsertOIDC path is unaffected.
//
// Constitutional invariants honored:
//
//   - P0-198-1: single transaction across all five inserts.
//   - P0-198-2: the first-user gate is the first read.
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
	// Should be unreachable — second attempt's first-user gate sees
	// the winning row and returns Bootstrapped=false.
	return BootstrapResult{Bootstrapped: false}, nil
}

// bootstrapAttempt runs one first-user gate + bootstrap-or-skip pass. The
// retry boolean is true when the partial UNIQUE index trip indicates a
// concurrent first-installer beat us; the caller re-enters the loop
// once.
func (s *Store) bootstrapAttempt(ctx context.Context, in BootstrapInput) (BootstrapResult, bool, error) {
	// READ COMMITTED is load-bearing: each statement takes a fresh
	// snapshot, so the gate below, read after the advisory lock is
	// granted, sees the user a concurrent winner committed while we
	// waited. Under REPEATABLE READ the snapshot would predate the lock
	// and the loser would bootstrap a second admin.
	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize concurrent first-installers. When the bootstrap tenant
	// already exists (seeded by bootstrap.sh) no tenants INSERT happens,
	// so the partial UNIQUE index cannot do it; the advisory lock does.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdvisoryLockKey); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap lock: %w", err)
	}

	// P0-198-2: the gate is the first read inside the tx so the
	// established-install path falls through without writes. The gate
	// is "nobody has been provisioned yet": no user and no super_admin.
	// The first user is the bootstrap admin, whether or not the bootstrap
	// tenant was seeded before them. Requiring no super_admins row too
	// keeps the gate shut on an install whose users were all deleted:
	// super_admins has no FK to users, so the grant outlives them.
	var provisioned bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users) OR EXISTS (SELECT 1 FROM super_admins)`,
	).Scan(&provisioned); err != nil {
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap check provisioned: %w", err)
	}
	if provisioned {
		// Established-install path — caller falls through to UpsertOIDC.
		return BootstrapResult{Bootstrapped: false}, false, nil
	}

	userID := uuid.New()

	// 1. The bootstrap tenant is canonical: attach the first user to the
	//    implied tenant, and create + flag it only when none exists.
	var tenantID uuid.UUID
	tenant, err := tenancy.ResolveImplicitTenant(ctx, tx)
	switch {
	case err == nil:
		tenantID = tenant.ID
		// Flag it as the bootstrap tenant unless some tenant already is.
		if _, err := tx.Exec(ctx,
			`UPDATE tenants SET is_bootstrap_tenant = true
			 WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM tenants WHERE is_bootstrap_tenant)`,
			tenantID,
		); err != nil {
			return BootstrapResult{}, false, fmt.Errorf("users: bootstrap flag tenant: %w", err)
		}
	case errors.Is(err, tenancy.ErrNoTenants):
		// The slice 144 partial UNIQUE index
		// `idx_tenants_bootstrap_singleton` is the second leg of defense
		// against concurrent first-installers.
		tenantID = uuid.New()
		if _, err := tx.Exec(ctx,
			`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, true)`,
			tenantID, DefaultBootstrapTenantName,
		); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
				// P0-198-3: concurrent first-installer beat us. Roll back,
				// caller re-enters loop, second pass sees the user and
				// returns Bootstrapped=false.
				return BootstrapResult{}, true, nil
			}
			return BootstrapResult{}, false, fmt.Errorf("users: bootstrap insert tenants: %w", err)
		}
	default:
		// Several tenants and no user: no tenant is implied, so the first
		// user cannot be placed. The wrapped *tenancy.AmbiguousTenantError
		// lists the tenants for the caller.
		return BootstrapResult{}, false, fmt.Errorf("users: bootstrap resolve tenant: %w", err)
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

// timingDummyHash is a password hash no login input is expected to match.
// VerifyLocalLogin verifies against it when the user or credential is
// missing, so an unknown email or tenant costs the same argon2id work as a
// wrong password and response time is no enumeration oracle (#1727).
var timingDummyHash = sync.OnceValue(func() string {
	h, err := password.Hash("atlas-login-timing-equalizer")
	if err != nil {
		return ""
	}
	return h
})

// burnPasswordVerify spends one password verification and discards the result.
func burnPasswordVerify(plaintext string) {
	if h := timingDummyHash(); h != "" {
		_, _ = password.Verify(plaintext, h)
	}
}

// VerifyLocalLogin returns the User on a successful (tenant, email, password)
// triple. Returns ErrInvalidCredentials when no user exists, the password
// does not verify, or the user is deactivated. The caller (HTTP login handler)
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
			burnPasswordVerify(plaintextPassword)
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	cred, err := q.GetLocalCredentialByUserID(ctx, dbx.GetLocalCredentialByUserIDParams{TenantID: tIDU, UserID: row.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			burnPasswordVerify(plaintextPassword)
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	ok, err := password.Verify(plaintextPassword, cred.PasswordHash)
	if err != nil {
		return User{}, err
	}
	// A deactivated user is refused only after the full verify, with the
	// same sentinel, so status is no oracle by response or timing (#1912).
	usr := userFromRow(row)
	if !ok || !usr.CanSignIn() {
		return User{}, ErrInvalidCredentials
	}
	return usr, nil
}

// CanSignIn reports whether the user may start a session. Admin
// revoke-membership sets status='disabled'; SCIM deprovision sets
// active=false. Either one refuses (#1912).
func (u User) CanSignIn() bool {
	return u.Status == "active" && u.Active
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
		Active:      row.Active,
		IdpIssuer:   row.IdpIssuer,
		IdpSubject:  row.IdpSubject,
		TimeZone:    row.TimeZone,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
}
