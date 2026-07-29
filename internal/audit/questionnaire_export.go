package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type QuestionnaireExportCounts struct {
	Manual          int
	ApprovedAI      int
	ExcludedDrafts  int
	ExportedAnswers int
}

type QuestionnaireExportEvent struct {
	Actor           string
	QuestionnaireID string
	Counts          QuestionnaireExportCounts
	OccurredAt      time.Time
}

type QuestionnaireExportWriter struct {
	pool *pgxpool.Pool
}

func NewQuestionnaireExportWriter(pool *pgxpool.Pool) *QuestionnaireExportWriter {
	return &QuestionnaireExportWriter{pool: pool}
}

func (w *QuestionnaireExportWriter) WriteQuestionnaireExport(ctx context.Context, event QuestionnaireExportEvent) error {
	if w == nil || w.pool == nil {
		return fmt.Errorf("audit: questionnaire export writer has no db pool")
	}
	if event.Actor == "" {
		return fmt.Errorf("audit: questionnaire export actor must be non-empty")
	}
	qid, err := uuid.Parse(event.QuestionnaireID)
	if err != nil {
		return fmt.Errorf("audit: questionnaire_id must be uuid: %w", err)
	}
	tenantID, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("audit: tenant id must be uuid: %w", err)
	}
	occurredAt := event.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO questionnaire_export_audit_log (
    id, tenant_id, questionnaire_id, actor,
    manual_count, approved_ai_count, excluded_draft_count, exported_answer_count,
    occurred_at, subject_module
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'core')`,
		uuid.New(), tenantUUID, qid, event.Actor,
		event.Counts.Manual, event.Counts.ApprovedAI, event.Counts.ExcludedDrafts, event.Counts.ExportedAnswers,
		occurredAt,
	)
	if err != nil {
		return fmt.Errorf("audit: insert questionnaire export audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}
