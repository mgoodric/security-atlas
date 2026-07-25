package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// AuditWriter persists one append-only ai_generations row per generation. It
// is the SHARED audit-record writer every AI-assist surface uses, so the
// model name+version+provider+prompt+context+draft record is captured
// identically across surfaces (the slice-182 audit discipline, made concrete
// and reusable).
//
// The writer is a pure DB layer over sqlc + the canonical tenant-scoped
// transaction pattern (tenancy.ApplyTenant): every write runs inside a tx
// with app.current_tenant set, so RLS scopes the INSERT to the caller's
// tenant (P0-498-8). Model output is bound as PARAMETERIZED values via sqlc
// (P0-498-7) -- the raw draft, system prompt, and context are never
// interpolated.
type AuditWriter struct {
	pool *pgxpool.Pool
}

// NewAuditWriter builds an AuditWriter over the given app-role pool.
func NewAuditWriter(pool *pgxpool.Pool) *AuditWriter {
	return &AuditWriter{pool: pool}
}

// ErrInvalidGeneration is returned when the record to persist is malformed
// (unknown surface, empty model provenance) BEFORE the DB round-trip, so a
// caller gets a clean Go error rather than a raw check_violation.
var ErrInvalidGeneration = errors.New("llm: invalid generation record")

// Generation is the input to AuditWriter.Write: the request provenance plus
// the resolved result. Callers assemble it from a GenerateRequest +
// GenerateResult pair (see RecordGeneration).
type Generation struct {
	Surface        Surface
	PromptVersion  string
	ModelName      string
	ModelVersion   string
	ModelProvider  string
	SystemPrompt   string
	ContextInputs  map[string]any
	RawDraft       string
	SurfaceSubject string
}

// validate enforces the same non-empty-provenance + known-surface invariants
// the DB CHECKs enforce, so the friendly Go error fires first.
func (g Generation) validate() error {
	if !ValidSurface(g.Surface) {
		return fmt.Errorf("%w: unknown surface %q", ErrInvalidGeneration, g.Surface)
	}
	if g.PromptVersion == "" || g.ModelName == "" || g.ModelVersion == "" || g.ModelProvider == "" {
		return fmt.Errorf("%w: prompt_version, model_name, model_version, model_provider are all required", ErrInvalidGeneration)
	}
	if g.SystemPrompt == "" {
		return fmt.Errorf("%w: system_prompt is required", ErrInvalidGeneration)
	}
	return nil
}

// Write persists one ai_generations row within the caller's tenant context and
// returns the stored row. The context MUST carry a tenant (tenancy.WithTenant);
// the row is INSERTed under app.current_tenant so RLS scopes it.
func (w *AuditWriter) Write(ctx context.Context, g Generation) (dbx.AiGeneration, error) {
	if err := g.validate(); err != nil {
		return dbx.AiGeneration{}, err
	}

	tenantID, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return dbx.AiGeneration{}, err
	}

	// Context inputs are stored as JSONB. An empty/nil map serializes to {}.
	ctxJSON := []byte("{}")
	if len(g.ContextInputs) > 0 {
		b, err := json.Marshal(g.ContextInputs)
		if err != nil {
			return dbx.AiGeneration{}, fmt.Errorf("%w: marshal context_inputs: %v", ErrInvalidGeneration, err)
		}
		ctxJSON = b
	}

	var out dbx.AiGeneration
	err = w.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, err := q.WriteAIGeneration(ctx, dbx.WriteAIGenerationParams{
			TenantID:       pgtype.UUID{Bytes: tenantID, Valid: true},
			Surface:        string(g.Surface),
			PromptVersion:  g.PromptVersion,
			ModelName:      g.ModelName,
			ModelVersion:   g.ModelVersion,
			ModelProvider:  g.ModelProvider,
			SystemPrompt:   g.SystemPrompt,
			ContextInputs:  ctxJSON,
			RawDraft:       g.RawDraft,
			SurfaceSubject: g.SurfaceSubject,
		})
		if err != nil {
			return err
		}
		out = row
		return nil
	})
	if err != nil {
		return dbx.AiGeneration{}, err
	}
	return out, nil
}

// ListBySubject returns all generations for one surface subject in the
// caller's tenant, newest first. Tenant-scoped via the same RLS transaction
// as Write.
func (w *AuditWriter) ListBySubject(ctx context.Context, surface Surface, subject string) ([]dbx.AiGeneration, error) {
	tenantID, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var rows []dbx.AiGeneration
	err = w.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		r, err := q.ListAIGenerationsBySubject(ctx, dbx.ListAIGenerationsBySubjectParams{
			TenantID:       pgtype.UUID{Bytes: tenantID, Valid: true},
			Surface:        string(surface),
			SurfaceSubject: subject,
		})
		if err != nil {
			return err
		}
		rows = r
		return nil
	})
	return rows, err
}

// CountForTenant returns the number of ai_generations rows visible to the
// caller's tenant. Used by the cross-tenant isolation test to prove tenant B
// sees zero of tenant A's rows.
func (w *AuditWriter) CountForTenant(ctx context.Context) (int64, error) {
	tenantID, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = w.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		c, err := q.CountAIGenerationsForTenant(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
		if err != nil {
			return err
		}
		count = c
		return nil
	})
	return count, err
}

// tenantUUIDFromContext resolves + parses the tenant id from the context.
func tenantUUIDFromContext(ctx context.Context) (uuid.UUID, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("llm: %w", err)
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("llm: parse tenant id: %w", err)
	}
	return tenantID, nil
}

// inTx runs fn inside a tenant-scoped transaction (the canonical
// tenancy.ApplyTenant pattern). The tx sets app.current_tenant so RLS scopes
// every query within fn to the caller's tenant.
func (w *AuditWriter) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("llm: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("llm: apply tenant: %w", err)
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("llm: commit: %w", err)
	}
	return nil
}
