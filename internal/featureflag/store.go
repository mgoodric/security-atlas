package featureflag

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Errors surfaced by Store. Handlers map these to HTTP status codes.
var (
	// ErrNotFound indicates the requested flag_key does not exist in the
	// Seed list (and therefore cannot be toggled -- only seeded keys are
	// valid toggle targets in v1). HTTP 404.
	ErrNotFound = errors.New("featureflag: unknown flag_key (not in Seed)")
	// ErrSpineForbidden indicates the flag_key matches a SpineForbiddenPrefix
	// and the Store will refuse to create or update it. HTTP 400.
	ErrSpineForbidden = errors.New("featureflag: spine-forbidden flag_key cannot be toggled")
	// ErrEmptyActor indicates the toggle caller did not supply an actor
	// identity. Defense in depth -- the handler should populate this from
	// the authenticated credential.
	ErrEmptyActor = errors.New("featureflag: actor is required for toggle audit")
)

// Flag is the public shape returned from Store methods. Combines the
// per-tenant row (when present) with the Seed default (the description /
// category are always sourced from Seed -- they are properties of the
// flag, not the tenant's toggle decision).
type Flag struct {
	Key           string
	Enabled       bool
	Description   string
	Category      string
	LastChangedBy string
	LastChangedAt time.Time
	// HasOverride is true when a feature_flags row exists for (tenant,
	// flag_key). When false, Enabled reflects the Seed default and the
	// audit-log will treat the first toggle as a from=default transition.
	HasOverride bool
}

// AuditEntry is the public shape of a feature_flag_audit_log row.
type AuditEntry struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	FlagKey     string
	FromEnabled bool
	ToEnabled   bool
	Actor       string
	Reason      string
	OccurredAt  time.Time
}

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Same shape as risk.Store / exception.Store / policy.Store: every method
// opens a tx, applies the tenant GUC, and runs queries inside that
// transaction.
//
// Field `pool` may be nil for unit tests that exercise the helper +
// Gate middleware without a DB; in that case Get / List / Set return
// the Seed default (Get) or an empty list (List) or ErrNoPool (Set).
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
// A nil pool is permitted -- Get/List degrade gracefully (Seed default)
// and Set returns an explicit error.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ErrNoPool is returned by Set when the Store has no pool wired. Tests
// that exercise the helper without a DB use this signal.
var ErrNoPool = errors.New("featureflag: store has no DB pool wired")

// Get returns the effective Flag for (current-tenant, key).
//
// Lookup order:
//  1. Try feature_flags row (under tenant tx).
//  2. On pgx.ErrNoRows -> return Seed default with HasOverride=false.
//  3. On any other DB error -> log warning, return Seed default with
//     HasOverride=false, swallow the error (anti-criterion P0: feature
//     flags MUST NOT fail closed; RLS is the security boundary).
//
// Returns ErrNotFound when the key is absent from the Seed list (so a
// typo'd key in a Gate(key) call surfaces at startup rather than
// silently allowing the route to serve).
func (s *Store) Get(ctx context.Context, key string) (Flag, error) {
	def, ok := DefaultByKey(key)
	if !ok {
		return Flag{}, ErrNotFound
	}

	// No pool -> return Seed default. Used by unit tests of Gate / Enabled.
	if s.pool == nil {
		return flagFromDefault(def), nil
	}

	var got Flag
	hasOverride := false
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetFeatureFlag(ctx, dbx.GetFeatureFlagParams{
			TenantID: pgUUID(tenantID),
			FlagKey:  key,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Missing row -> Seed default. Not an error.
				return nil
			}
			return fmt.Errorf("get feature flag: %w", err)
		}
		hasOverride = true
		got = flagFromRow(row, def)
		return nil
	})
	if err != nil {
		// Anti-criterion P0: never fail closed. Log the warning and
		// return the Seed default. The operator sees a degraded service
		// (no audit-log entry for the failed lookup) but no capability
		// is silently disabled.
		log.Printf("featureflag: DB read failed for key=%q: %v -- falling back to seed default", key, err)
		return flagFromDefault(def), nil
	}
	if !hasOverride {
		return flagFromDefault(def), nil
	}
	got.HasOverride = true
	return got, nil
}

// List returns every flag for the current tenant. Always returns one
// entry per Seed entry -- tenants who have never toggled see only Seed
// defaults; tenants who have toggled some see overrides merged with
// defaults for the rest.
func (s *Store) List(ctx context.Context) ([]Flag, error) {
	out := make([]Flag, 0, len(Seed))
	overrides := map[string]Flag{}
	if s.pool != nil {
		err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
			rows, err := q.ListFeatureFlags(ctx, pgUUID(tenantID))
			if err != nil {
				return fmt.Errorf("list feature flags: %w", err)
			}
			for _, r := range rows {
				def, ok := DefaultByKey(r.FlagKey)
				if !ok {
					// Row whose key was removed from Seed -- skip. The
					// audit log preserves the history; the row is dead.
					continue
				}
				overrides[r.FlagKey] = flagFromRow(r, def)
			}
			return nil
		})
		if err != nil {
			// Anti-criterion P0: log, fall back to Seed defaults only.
			log.Printf("featureflag: DB list failed: %v -- returning seed defaults", err)
			overrides = nil
		}
	}
	for _, d := range Seed {
		if o, ok := overrides[d.Key]; ok {
			o.HasOverride = true
			out = append(out, o)
			continue
		}
		out = append(out, flagFromDefault(d))
	}
	return out, nil
}

// Set writes a toggle (enabled / disabled) for (current-tenant, key) and
// records an audit-log row in the same transaction. The audit row has
// from_enabled = the previous effective value (the row's existing
// enabled column, or the Seed default if no row existed).
//
// Returns ErrNotFound when the key is absent from the Seed list,
// ErrSpineForbidden when the key matches a SpineForbiddenPrefix,
// ErrEmptyActor when actor is empty. DB errors are NOT swallowed by
// Set (unlike Get / List) -- a toggle is an explicit operator action
// and silent failure would be worse than a 5xx.
func (s *Store) Set(ctx context.Context, key string, enabled bool, actor, reason string) (Flag, error) {
	def, ok := DefaultByKey(key)
	if !ok {
		return Flag{}, ErrNotFound
	}
	if IsSpineForbidden(key) {
		// Defense in depth: the Seed unit test prevents this from
		// reaching production, but if a future contributor introduces
		// a spine key into Seed AND somehow bypasses the unit test,
		// the Store still refuses to write it.
		return Flag{}, ErrSpineForbidden
	}
	if actor == "" {
		return Flag{}, ErrEmptyActor
	}
	if s.pool == nil {
		return Flag{}, ErrNoPool
	}

	now := time.Now().UTC()
	var out Flag
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Resolve the previous effective value for the audit log. The
		// existing row's `enabled` if present; otherwise the Seed
		// default.
		fromEnabled := def.Enabled
		existing, gErr := q.GetFeatureFlag(ctx, dbx.GetFeatureFlagParams{
			TenantID: pgUUID(tenantID),
			FlagKey:  key,
		})
		if gErr != nil && !errors.Is(gErr, pgx.ErrNoRows) {
			return fmt.Errorf("read existing flag: %w", gErr)
		}
		if gErr == nil {
			fromEnabled = existing.Enabled
		}

		// Upsert the row.
		row, err := q.UpsertFeatureFlag(ctx, dbx.UpsertFeatureFlagParams{
			TenantID:      pgUUID(tenantID),
			FlagKey:       key,
			Enabled:       enabled,
			Description:   def.Description,
			Category:      def.Category,
			LastChangedBy: stringPtr(actor),
			LastChangedAt: pgTimestamptz(now),
		})
		if err != nil {
			return fmt.Errorf("upsert feature flag: %w", err)
		}

		// Write the audit-log row (append-only).
		if _, alErr := q.WriteFeatureFlagAuditLog(ctx, dbx.WriteFeatureFlagAuditLogParams{
			ID:          pgUUID(uuid.New()),
			TenantID:    pgUUID(tenantID),
			FlagKey:     key,
			FromEnabled: fromEnabled,
			ToEnabled:   enabled,
			Actor:       actor,
			Reason:      reason,
		}); alErr != nil {
			return fmt.Errorf("write feature_flag_audit_log: %w", alErr)
		}

		out = flagFromRow(row, def)
		out.HasOverride = true
		return nil
	})
	return out, err
}

// AuditLog returns every audit-log row for the current tenant, newest
// first. Powers "who toggled what and when" views and the integration
// test that asserts AC-10.
func (s *Store) AuditLog(ctx context.Context) ([]AuditEntry, error) {
	if s.pool == nil {
		return nil, ErrNoPool
	}
	var out []AuditEntry
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListFeatureFlagAuditLog(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list feature_flag_audit_log: %w", err)
		}
		out = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out[i] = auditFromRow(r)
		}
		return nil
	})
	return out, err
}

// ----- tenancy plumbing -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("featureflag: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("featureflag: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("featureflag: commit: %w", err)
	}
	return nil
}

// ----- row conversion -----

func flagFromDefault(d Default) Flag {
	return Flag{
		Key:         d.Key,
		Enabled:     d.Enabled,
		Description: d.Description,
		Category:    d.Category,
		HasOverride: false,
	}
}

func flagFromRow(r dbx.FeatureFlag, d Default) Flag {
	out := Flag{
		Key:         r.FlagKey,
		Enabled:     r.Enabled,
		Description: d.Description,
		Category:    d.Category,
		HasOverride: true,
	}
	if r.LastChangedBy != nil {
		out.LastChangedBy = *r.LastChangedBy
	}
	if r.LastChangedAt.Valid {
		out.LastChangedAt = r.LastChangedAt.Time
	}
	return out
}

func auditFromRow(r dbx.FeatureFlagAuditLog) AuditEntry {
	out := AuditEntry{
		ID:          uuid.UUID(r.ID.Bytes),
		TenantID:    uuid.UUID(r.TenantID.Bytes),
		FlagKey:     r.FlagKey,
		FromEnabled: r.FromEnabled,
		ToEnabled:   r.ToEnabled,
		Actor:       r.Actor,
		Reason:      r.Reason,
	}
	if r.OccurredAt.Valid {
		out.OccurredAt = r.OccurredAt.Time
	}
	return out
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
