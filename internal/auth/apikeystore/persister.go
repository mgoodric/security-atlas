// Persister adapts the slice-034 api_keys table to the credstore.Persister
// interface (OE-435, chaos gap G-1): the in-memory credstore writes issues,
// rotations and revocations through here and rehydrates itself at boot, so
// a push credential issued to a connector or CI job survives an atlas
// process restart.
//
// Storage properties are inherited from the existing table, not invented:
//   - tokens are hashed at rest with HMAC-SHA256(BEARER_HASH_KEY) per
//     ADR 0002 — no plaintext bearer is ever persisted;
//   - rows are tenant-scoped and RLS-enforced (constitutional invariant
//     #6); every write runs on the atlas_app pool inside a
//     tenancy.ApplyTenant transaction;
//   - only LoadActive uses the BYPASSRLS authPool, mirroring the existing
//     GetAPIKeyByHash posture: it runs once at boot, before any tenant
//     context exists, and the tenant_id each row carries is what every
//     subsequent authorization decision is scoped by.

package apikeystore

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
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

// persistTimeout bounds each write-through call. The credstore holds its
// mutex across these calls, so a hung DB must not hang authentication
// forever; issuance is rare and 5s is generous for a single-row tx.
const persistTimeout = 5 * time.Second

// loadTimeout bounds the one-shot boot scan.
const loadTimeout = 30 * time.Second

// Persister implements credstore.Persister over api_keys.
type Persister struct {
	pool     *pgxpool.Pool
	authPool *pgxpool.Pool
	hasher   *bearer.Hasher
}

// NewPersister constructs a Persister. `pool` is the RLS-enforced
// application pool (atlas_app) used for all writes; `authPool` is the
// BYPASSRLS pool used ONLY by the LoadActive boot scan. If authPool is nil
// it falls back to pool — under which the scan sees zero rows (FORCE RLS
// with no tenant context), so cmd/atlas warns loudly in that configuration.
func NewPersister(pool, authPool *pgxpool.Pool, hasher *bearer.Hasher) *Persister {
	if authPool == nil {
		authPool = pool
	}
	return &Persister{pool: pool, authPool: authPool, hasher: hasher}
}

// NewID returns a fresh "key_<uuid>" credential id. The uuid suffix is the
// api_keys primary key, so the id round-trips exactly through persistence.
func (p *Persister) NewID() (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("apikeystore: new id: %w", err)
	}
	return "key_" + u.String(), nil
}

// HashToken returns the hex-encoded HMAC-SHA256 of the bearer plaintext —
// the same at-rest hash the slice-034 Store persists (ADR 0002).
func (p *Persister) HashToken(token string) string {
	return hex.EncodeToString(p.hasher.Hash(token))
}

// Insert durably records a newly issued credential.
func (p *Persister) Insert(rec credstore.PersistedCredential) error {
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	return p.withTenantTx(ctx, rec.Cred.TenantID, func(q *dbx.Queries) error {
		return p.insertRow(ctx, q, rec)
	})
}

// Rotate durably records a rotation in one transaction: insert the
// successor row and set the predecessor's retires_at.
func (p *Persister) Rotate(successor credstore.PersistedCredential, predecessorID, tenantID string, retiresAt time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	predID, err := keyIDToPg(predecessorID)
	if err != nil {
		return err
	}
	tid, err := uuidToPg(tenantID)
	if err != nil {
		return err
	}
	return p.withTenantTx(ctx, tenantID, func(q *dbx.Queries) error {
		if err := p.insertRow(ctx, q, successor); err != nil {
			return err
		}
		return q.SetAPIKeyRetiresAt(ctx, dbx.SetAPIKeyRetiresAtParams{
			TenantID:  tid,
			ID:        predID,
			RetiresAt: pgtype.Timestamptz{Time: retiresAt, Valid: true},
		})
	})
}

// Revoke durably marks the credential revoked.
func (p *Persister) Revoke(id, tenantID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	keyID, err := keyIDToPg(id)
	if err != nil {
		return err
	}
	tid, err := uuidToPg(tenantID)
	if err != nil {
		return err
	}
	return p.withTenantTx(ctx, tenantID, func(q *dbx.Queries) error {
		return q.RevokeAPIKey(ctx, dbx.RevokeAPIKeyParams{TenantID: tid, ID: keyID})
	})
}

// LoadActive returns every credential that should authenticate right now:
// not revoked, not expired, not past its rotation grace. Runs on the
// BYPASSRLS authPool because it executes at boot, before any tenant
// context exists (same posture as the GetAPIKeyByHash auth path).
func (p *Persister) LoadActive() ([]credstore.PersistedCredential, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	rows, err := dbx.New(p.authPool).ListAllActiveAPIKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("apikeystore: load active keys: %w", err)
	}
	now := time.Now().UTC()
	out := make([]credstore.PersistedCredential, 0, len(rows))
	for _, row := range rows {
		if row.ExpiresAt.Valid && now.After(row.ExpiresAt.Time) {
			continue
		}
		if row.RetiresAt.Valid && now.After(row.RetiresAt.Time) {
			continue
		}
		pc := credstore.PersistedCredential{
			Cred:         credentialFromRow(row),
			TokenHashHex: hex.EncodeToString(row.TokenHash),
		}
		if row.RetiresAt.Valid {
			pc.RetiresAt = row.RetiresAt.Time
		}
		out = append(out, pc)
	}
	return out, nil
}

// withTenantTx runs fn inside an RLS-scoped transaction for tenantID on
// the application pool.
func (p *Persister) withTenantTx(ctx context.Context, tenantID string, fn func(q *dbx.Queries) error) error {
	tctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("apikeystore: tenant context: %w", err)
	}
	tx, err := p.pool.BeginTx(tctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(tctx) }()
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		return err
	}
	if err := fn(dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(tctx)
}

// insertRow maps a credstore credential onto an api_keys INSERT. The
// mapping must round-trip through credentialFromRow: everything the
// credstore hands out at issue time is exactly what a post-restart load
// reconstructs.
func (p *Persister) insertRow(ctx context.Context, q *dbx.Queries, rec credstore.PersistedCredential) error {
	id, err := keyIDToPg(rec.Cred.ID)
	if err != nil {
		return err
	}
	tid, err := uuidToPg(rec.Cred.TenantID)
	if err != nil {
		return err
	}
	hash, err := hex.DecodeString(rec.TokenHashHex)
	if err != nil {
		return fmt.Errorf("apikeystore: token hash decode: %w", err)
	}
	scope := []byte(rec.Cred.ScopePredicate)
	if len(scope) == 0 {
		scope = []byte("{}")
	}
	kinds := rec.Cred.Kinds
	if kinds == nil {
		kinds = []string{}
	}
	roles := rec.Cred.OwnerRoles
	if roles == nil {
		roles = []string{}
	}
	var expiresAt pgtype.Timestamptz
	if rec.Cred.TTL > 0 {
		expiresAt = pgtype.Timestamptz{Time: rec.Cred.IssuedAt.Add(rec.Cred.TTL), Valid: true}
	}
	var rotatedFrom pgtype.UUID
	if rec.Cred.RotatedFrom != "" {
		rotatedFrom, err = keyIDToPg(rec.Cred.RotatedFrom)
		if err != nil {
			return err
		}
	}
	// credstore defaults UserID to the credential id itself ("key_..."),
	// which credentialFromRow reconstructs from a NULL issued_by. A UserID
	// that parses as a bare uuid (the slice-034 OIDC issue path) is a real
	// users(id) and rides in issued_by so it round-trips too.
	var issuedBy pgtype.UUID
	if u, uerr := uuid.Parse(rec.Cred.UserID); uerr == nil {
		issuedBy = pgtype.UUID{Bytes: u, Valid: true}
	}
	_, err = q.InsertAPIKey(ctx, dbx.InsertAPIKeyParams{
		ID:             id,
		TenantID:       tid,
		TokenHash:      hash,
		ScopePredicate: scope,
		AllowedKinds:   kinds,
		IssuedBy:       issuedBy,
		ExpiresAt:      expiresAt,
		RotatedFrom:    rotatedFrom,
		IsAdmin:        rec.Cred.IsAdmin,
		IsApprover:     rec.Cred.IsApprover,
		OwnerRoles:     roles,
		Last4:          rec.Cred.Last4,
		TtlSeconds:     int64(rec.Cred.TTL.Seconds()),
	})
	return err
}

// keyIDToPg parses a "key_<uuid>" credential id into the api_keys uuid
// primary key.
func keyIDToPg(keyID string) (pgtype.UUID, error) {
	raw, ok := strings.CutPrefix(keyID, "key_")
	if !ok {
		return pgtype.UUID{}, fmt.Errorf("apikeystore: credential id %q lacks key_ prefix", keyID)
	}
	u, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("apikeystore: credential id %q: %w", keyID, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

// Compile-time interface check.
var _ credstore.Persister = (*Persister)(nil)
