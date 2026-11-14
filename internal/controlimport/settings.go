package controlimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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

// SetSettings stores the caller's tenant's settings.
func (s *Store) SetSettings(ctx context.Context, in Settings, updatedBy string) (Settings, error) {
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
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
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return in, nil
}
