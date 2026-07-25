package cloud

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the tenant-scoped persistence layer for the per-tenant routing
// config. Every read/write runs inside a tenant-scoped transaction
// (tenancy.ApplyTenant sets app.current_tenant), so the four-policy RLS on
// tenant_llm_routing scopes the row to the caller's tenant (P0-499-5 / AC-10).
//
// The provider API key is WRITE-ONLY: Set encrypts the plaintext before it ever
// touches the DB, and there is no method that returns the plaintext to an API
// caller. Resolve (used only by the Router at generation time) decrypts the key
// into a masked Secret for the transport boundary; the masked Config view used
// by the API handler carries only a boolean "key present" flag (AC-3 / AC-11).
type Store struct {
	pool    *pgxpool.Pool
	crypter *Crypter
}

// NewStore builds a Store over the app-role pool and a Crypter. crypter may be
// nil on a deployment with no cloud master key configured; in that case Set
// rejects a cloud opt-in with ErrCrypterUnconfigured (you cannot store a key
// you cannot protect), while reading/clearing config still works.
func NewStore(pool *pgxpool.Pool, crypter *Crypter) *Store {
	return &Store{pool: pool, crypter: crypter}
}

// ErrCloudKeyRequired is returned by Set when a cloud provider is selected
// without an API key. The DB CHECK also enforces this; the Go check gives a
// clean error before the round-trip.
var ErrCloudKeyRequired = errors.New("cloud: a cloud provider requires an api key")

// ErrLocalProviderNoKey is returned by Set when an API key is supplied for the
// local-ollama provider (a confused request — local needs no key).
var ErrLocalProviderNoKey = errors.New("cloud: local-ollama provider takes no api key")

// MaskedConfig is the API-safe view of a tenant's routing config. It NEVER
// carries the key — only whether one is configured. This is the only shape the
// admin endpoint returns (P0-499-4 / AC-11).
type MaskedConfig struct {
	Provider     Provider `json:"provider"`
	HasAPIKey    bool     `json:"has_api_key"`
	IsCloud      bool     `json:"is_cloud"`
	APIKeyMasked string   `json:"api_key,omitempty"`
}

// Get returns the tenant's MASKED routing config. When the tenant has no row,
// it returns the default (local-ollama, no key) — the off-by-default posture
// (AC-2). The plaintext key is never read here.
func (s *Store) Get(ctx context.Context) (MaskedConfig, error) {
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return MaskedConfig{}, err
	}
	var row dbx.TenantLlmRouting
	found := false
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		r, gerr := q.GetTenantLLMRouting(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
		if gerr != nil {
			if errors.Is(gerr, pgxNoRows) {
				return nil // no row => default
			}
			return gerr
		}
		row = r
		found = true
		return nil
	})
	if err != nil {
		return MaskedConfig{}, err
	}
	if !found {
		return MaskedConfig{Provider: ProviderLocalOllama, HasAPIKey: false, IsCloud: false}, nil
	}
	return maskRow(row), nil
}

// Set inserts or replaces the tenant's routing config. For a cloud provider the
// plaintext key is ENCRYPTED here and the ciphertext stored; the plaintext is
// never persisted, returned, or logged (P0-499-4). For local-ollama the key is
// cleared. Requires the tenant context (RLS scopes the write).
func (s *Store) Set(ctx context.Context, provider Provider, apiKey Secret) (MaskedConfig, error) {
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return MaskedConfig{}, err
	}
	if !provider.IsCloud() && provider != ProviderLocalOllama {
		return MaskedConfig{}, fmt.Errorf("cloud: unknown provider %q", provider)
	}

	var ciphertext *string
	if provider.IsCloud() {
		if apiKey.IsZero() {
			return MaskedConfig{}, ErrCloudKeyRequired
		}
		if s.crypter == nil {
			return MaskedConfig{}, ErrCrypterUnconfigured
		}
		ct, eerr := s.crypter.Encrypt(apiKey)
		if eerr != nil {
			return MaskedConfig{}, eerr
		}
		ciphertext = &ct
	} else {
		// local-ollama: no key permitted (matches the DB key-presence CHECK).
		if !apiKey.IsZero() {
			return MaskedConfig{}, ErrLocalProviderNoKey
		}
	}

	var row dbx.TenantLlmRouting
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		r, werr := q.UpsertTenantLLMRouting(ctx, dbx.UpsertTenantLLMRoutingParams{
			TenantID:         pgtype.UUID{Bytes: tenantID, Valid: true},
			Provider:         provider.String(),
			ApiKeyCiphertext: ciphertext,
		})
		if werr != nil {
			return werr
		}
		row = r
		return nil
	})
	if err != nil {
		return MaskedConfig{}, err
	}
	return maskRow(row), nil
}

// Clear removes the tenant's routing config, reverting to the local-ollama
// default. Returns true when a row was removed, false when none existed.
func (s *Store) Clear(ctx context.Context) (bool, error) {
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return false, err
	}
	var removed int64
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		n, derr := q.DeleteTenantLLMRouting(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
		if derr != nil {
			return derr
		}
		removed = n
		return nil
	})
	if err != nil {
		return false, err
	}
	return removed > 0, nil
}

// Resolve returns the tenant's provider and, for a cloud provider, the
// DECRYPTED key as a masked Secret. It is used ONLY by the Router at generation
// time — never by an API handler. When the tenant has no row it returns
// local-ollama with an empty key (the default). A decrypt failure surfaces
// ErrDecrypt (the key bytes are never echoed).
func (s *Store) Resolve(ctx context.Context) (Provider, Secret, error) {
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return "", "", err
	}
	var row dbx.TenantLlmRouting
	found := false
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		r, gerr := q.GetTenantLLMRouting(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
		if gerr != nil {
			if errors.Is(gerr, pgxNoRows) {
				return nil
			}
			return gerr
		}
		row = r
		found = true
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if !found {
		return ProviderLocalOllama, "", nil
	}
	provider, ok := ParseProvider(row.Provider)
	if !ok {
		// A row whose provider is outside the enum is impossible given the DB
		// CHECK, but fail closed to local rather than route to an unknown
		// backend.
		return ProviderLocalOllama, "", nil
	}
	if !provider.IsCloud() {
		return provider, "", nil
	}
	if row.ApiKeyCiphertext == nil || *row.ApiKeyCiphertext == "" {
		// Cloud provider with no key is impossible (DB CHECK); fail closed.
		return "", "", ErrCloudKeyRequired
	}
	if s.crypter == nil {
		return "", "", ErrCrypterUnconfigured
	}
	key, derr := s.crypter.Decrypt(*row.ApiKeyCiphertext)
	if derr != nil {
		return "", "", derr
	}
	return provider, key, nil
}

// maskRow converts a stored row into the API-safe MaskedConfig (no plaintext,
// no ciphertext — only "key present").
func maskRow(row dbx.TenantLlmRouting) MaskedConfig {
	provider, _ := ParseProvider(row.Provider)
	has := row.ApiKeyCiphertext != nil && *row.ApiKeyCiphertext != ""
	mc := MaskedConfig{
		Provider:  provider,
		HasAPIKey: has,
		IsCloud:   provider.IsCloud(),
	}
	if has {
		mc.APIKeyMasked = redactedPlaceholder
	}
	return mc
}

// tenantUUID resolves + parses the tenant id from the context.
func tenantUUID(ctx context.Context) (uuid.UUID, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("cloud: %w", err)
	}
	id, err := uuid.Parse(tenantStr)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("cloud: parse tenant id: %w", err)
	}
	return id, nil
}

// inTx runs fn inside a tenant-scoped transaction (the canonical
// tenancy.ApplyTenant pattern). app.current_tenant is set so RLS scopes every
// query within fn to the caller's tenant.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cloud: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("cloud: apply tenant: %w", err)
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cloud: commit: %w", err)
	}
	return nil
}
