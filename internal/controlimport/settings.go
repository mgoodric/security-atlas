package controlimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Settings is a tenant's control import configuration (#2079).
type Settings struct {
	// LLMRerankLocal opts a tenant that is not routed to a cloud LLM in to
	// reranking import candidates on its local Ollama. Off by default: the
	// local default can't tell an installed Ollama from a missing one, so an
	// administrator says that one is there.
	LLMRerankLocal bool `json:"llm_rerank_local"`
}

// OptInGate is the rerank gate: a tenant routed to a cloud LLM is reranked
// (its cloud opt-in allows it), and so is one whose settings opt in to
// reranking on its local Ollama. Every other tenant is not asked.
func OptInGate(cloudOptedIn RerankGate, settings func(context.Context) (Settings, error)) RerankGate {
	return func(ctx context.Context) (bool, error) {
		cloud, err := cloudOptedIn(ctx)
		if err != nil || cloud {
			return cloud, err
		}
		s, err := settings(ctx)
		if err != nil {
			return false, err
		}
		return s.LLMRerankLocal, nil
	}
}

// GetSettings reads the caller's tenant's settings; a tenant with no row has
// the defaults.
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var out Settings
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		err := tx.QueryRow(ctx, `SELECT llm_rerank_local FROM control_import_settings`).Scan(&out.LLMRerankLocal)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("controlimport: read settings: %w", err)
		}
		return nil
	})
	return out, err
}

// AuditActionSettingsUpdate is the me_audit_log action written for every
// settings change (#2224). CHECK constraint extended in migration
// 20261120000000_llm_settings_audit.sql.
const AuditActionSettingsUpdate = "control_import_settings_update"

// SetSettings stores the caller's tenant's settings and, in the same
// transaction, writes one me_audit_log row naming actor and the before and
// after values (#2224). The setting decides whether tenant text is sent to an
// LLM, so its history is audit evidence. actor is uuid.Nil for a caller that
// is not a user (a bootstrap key); principal is the acting credential's
// credstore.PrincipalOf, which names it either way (#2329).
func (s *Store) SetSettings(ctx context.Context, in Settings, updatedBy string, actor uuid.UUID, principal string) (Settings, error) {
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
		var before Settings
		err := tx.QueryRow(ctx, `SELECT llm_rerank_local FROM control_import_settings FOR UPDATE`).Scan(&before.LLMRerankLocal)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("controlimport: read settings: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO control_import_settings (tenant_id, llm_rerank_local, updated_by)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (tenant_id) DO UPDATE
			SET llm_rerank_local = EXCLUDED.llm_rerank_local,
			    updated_by = EXCLUDED.updated_by,
			    updated_at = now()
		`, tenantID, in.LLMRerankLocal, updatedBy); err != nil {
			return fmt.Errorf("controlimport: write settings: %w", err)
		}
		beforeBlob, err := json.Marshal(before)
		if err != nil {
			return fmt.Errorf("controlimport: audit before: %w", err)
		}
		afterBlob, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("controlimport: audit after: %w", err)
		}
		tenant, err := uuid.Parse(tenantID)
		if err != nil {
			return fmt.Errorf("controlimport: parse tenant id: %w", err)
		}
		if err := dbx.New(tx).InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID:       pgtype.UUID{Bytes: tenant, Valid: true},
			UserID:         pgtype.UUID{Bytes: actor, Valid: true},
			Action:         AuditActionSettingsUpdate,
			Before:         beforeBlob,
			After:          afterBlob,
			ActorPrincipal: nullable(principal),
		}); err != nil {
			return fmt.Errorf("controlimport: audit settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return in, nil
}
