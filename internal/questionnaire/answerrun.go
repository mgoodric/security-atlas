package questionnaire

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	AnswerRunStatusPending   = "pending"
	AnswerRunStatusRunning   = "running"
	AnswerRunStatusCompleted = "completed"
	AnswerRunStatusFailed    = "failed"
	AnswerRunStatusCanceled  = "canceled"

	AnswerRunOutcomeDrafted                = "drafted"
	AnswerRunOutcomeInsufficientEvidence   = "insufficient_evidence"
	AnswerRunOutcomeSuppressed             = "suppressed"
	AnswerRunOutcomeSkippedNeedsMapping    = "skipped_needs_mapping"
	AnswerRunOutcomeSkippedAlreadyAnswered = "skipped_already_answered"
	AnswerRunOutcomeError                  = "error"

	AnswerRunReasonNeedsMapping    = "needs_mapping"
	AnswerRunReasonAlreadyAnswered = "already_answered"
	AnswerRunReasonRowCapExceeded  = "row_cap_exceeded"
	AnswerRunReasonSuggestError    = "suggest_error"
	AnswerRunReasonRunCanceled     = "run_canceled"
)

// AnswerRunRowCap bounds one request-scoped batch run. The value is recorded
// onto each run so a future cap change does not rewrite old execution meaning.
const AnswerRunRowCap = 100

var (
	ErrActiveAnswerRun       = errors.New("questionnaire answer run already active")
	ErrAnswerRunNotFound     = errors.New("questionnaire answer run not found")
	ErrQuestionnaireNotFound = errors.New("questionnaire not found")
)

type AnswerRun struct {
	ID                string    `json:"id"`
	QuestionnaireID   string    `json:"questionnaire_id"`
	Status            string    `json:"status"`
	StartedBy         string    `json:"started_by"`
	RowCap            int32     `json:"row_cap"`
	TotalCount        int32     `json:"total_count"`
	DraftedCount      int32     `json:"drafted_count"`
	InsufficientCount int32     `json:"insufficient_count"`
	SuppressedCount   int32     `json:"suppressed_count"`
	SkippedCount      int32     `json:"skipped_count"`
	ErrorCount        int32     `json:"error_count"`
	FailureReason     string    `json:"failure_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type AnswerRunItem struct {
	ID              string    `json:"id"`
	RunID           string    `json:"run_id"`
	QuestionnaireID string    `json:"questionnaire_id"`
	QuestionID      string    `json:"question_id"`
	SortOrder       int32     `json:"sort_order"`
	Outcome         string    `json:"outcome"`
	ReasonCode      string    `json:"reason_code,omitempty"`
	AnswerID        string    `json:"answer_id,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AnswerRunDetail struct {
	Run   AnswerRun       `json:"run"`
	Items []AnswerRunItem `json:"items"`
}

type answerRunQuestion struct {
	ID           uuid.UUID
	SortOrder    int32
	NeedsMapping bool
	HasAnswer    bool
}

type AnswerRunStore struct {
	pool *pgxpool.Pool
}

func NewAnswerRunStore(pool *pgxpool.Pool) *AnswerRunStore {
	return &AnswerRunStore{pool: pool}
}

func (s *AnswerRunStore) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("questionnaire answer run: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("questionnaire answer run: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("questionnaire answer run: commit: %w", err)
	}
	return nil
}

func (s *AnswerRunStore) CreateRun(ctx context.Context, questionnaireID uuid.UUID, startedBy string, rowCap int) (AnswerRun, error) {
	var out AnswerRun
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM questionnaires WHERE tenant_id = $1 AND id = $2)
		`, pgUUIDVal(tenantID), pgUUIDVal(questionnaireID)).Scan(&exists); err != nil {
			return fmt.Errorf("questionnaire answer run: check questionnaire: %w", err)
		}
		if !exists {
			return ErrQuestionnaireNotFound
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO questionnaire_answer_runs
				(id, tenant_id, questionnaire_id, status, started_by, row_cap)
			VALUES ($1, $2, $3, 'pending', $4, $5)
			RETURNING id::text, questionnaire_id::text, status, started_by, row_cap,
				total_count, drafted_count, insufficient_count, suppressed_count,
				skipped_count, error_count, failure_reason, created_at, updated_at
		`, pgUUIDVal(uuid.New()), pgUUIDVal(tenantID), pgUUIDVal(questionnaireID), startedBy, rowCap)
		if err := scanAnswerRun(row, &out); err != nil {
			if isUniqueViolation(err) {
				return ErrActiveAnswerRun
			}
			return fmt.Errorf("questionnaire answer run: insert: %w", err)
		}
		return nil
	})
	return out, err
}

func (s *AnswerRunStore) MarkRunning(ctx context.Context, runID uuid.UUID) error {
	return s.updateStatus(ctx, runID, AnswerRunStatusRunning, "")
}

func (s *AnswerRunStore) Complete(ctx context.Context, runID uuid.UUID) error {
	return s.updateStatus(ctx, runID, AnswerRunStatusCompleted, "")
}

func (s *AnswerRunStore) Fail(ctx context.Context, runID uuid.UUID, reason string) error {
	return s.updateStatus(ctx, runID, AnswerRunStatusFailed, reason)
}

func (s *AnswerRunStore) Cancel(ctx context.Context, runID uuid.UUID) error {
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		tag, err := tx.Exec(ctx, `
			UPDATE questionnaire_answer_runs
			SET status = 'canceled', canceled_at = now()
			WHERE tenant_id = $1
			  AND id = $2
			  AND status IN ('pending', 'running')
		`, pgUUIDVal(tenantID), pgUUIDVal(runID))
		if err != nil {
			return fmt.Errorf("questionnaire answer run: cancel: %w", err)
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(SELECT 1 FROM questionnaire_answer_runs WHERE tenant_id = $1 AND id = $2)
			`, pgUUIDVal(tenantID), pgUUIDVal(runID)).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrAnswerRunNotFound
			}
		}
		return nil
	})
	return err
}

func (s *AnswerRunStore) updateStatus(ctx context.Context, runID uuid.UUID, status, reason string) error {
	return s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		switch status {
		case AnswerRunStatusRunning:
			_, err := tx.Exec(ctx, `
				UPDATE questionnaire_answer_runs
				SET status = 'running', started_at = COALESCE(started_at, now())
				WHERE tenant_id = $1 AND id = $2
			`, pgUUIDVal(tenantID), pgUUIDVal(runID))
			return err
		case AnswerRunStatusCompleted:
			_, err := tx.Exec(ctx, `
				UPDATE questionnaire_answer_runs
				SET status = 'completed', completed_at = now()
				WHERE tenant_id = $1 AND id = $2
			`, pgUUIDVal(tenantID), pgUUIDVal(runID))
			return err
		case AnswerRunStatusFailed:
			_, err := tx.Exec(ctx, `
				UPDATE questionnaire_answer_runs
				SET status = 'failed', failed_at = now(), failure_reason = $3
				WHERE tenant_id = $1 AND id = $2
			`, pgUUIDVal(tenantID), pgUUIDVal(runID), boundRunMessage(reason))
			return err
		default:
			return fmt.Errorf("questionnaire answer run: unsupported status %q", status)
		}
	})
}

func (s *AnswerRunStore) IsCanceled(ctx context.Context, runID uuid.UUID) (bool, error) {
	var canceled bool
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		err := tx.QueryRow(ctx, `
			SELECT status = 'canceled'
			FROM questionnaire_answer_runs
			WHERE tenant_id = $1 AND id = $2
		`, pgUUIDVal(tenantID), pgUUIDVal(runID)).Scan(&canceled)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAnswerRunNotFound
		}
		return err
	})
	return canceled, err
}

func (s *AnswerRunStore) Questions(ctx context.Context, questionnaireID uuid.UUID) ([]answerRunQuestion, error) {
	var out []answerRunQuestion
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT qq.id, qq.sort_order, qq.scf_anchor_id IS NULL, qa.id IS NOT NULL
			FROM questionnaire_questions qq
			LEFT JOIN questionnaire_answers qa
			  ON qa.tenant_id = qq.tenant_id AND qa.question_id = qq.id
			WHERE qq.tenant_id = $1 AND qq.questionnaire_id = $2
			ORDER BY qq.sort_order ASC, qq.id ASC
		`, pgUUIDVal(tenantID), pgUUIDVal(questionnaireID))
		if err != nil {
			return fmt.Errorf("questionnaire answer run: list questions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var q answerRunQuestion
			var id pgtype.UUID
			if err := rows.Scan(&id, &q.SortOrder, &q.NeedsMapping, &q.HasAnswer); err != nil {
				return err
			}
			q.ID = uuid.UUID(id.Bytes)
			out = append(out, q)
		}
		return rows.Err()
	})
	return out, err
}

func (s *AnswerRunStore) InsertItem(ctx context.Context, runID, questionnaireID, questionID uuid.UUID, sortOrder int32, outcome, reason, answerID, message string) error {
	return s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		var answer *pgtype.UUID
		if answerID != "" {
			parsed, err := uuid.Parse(answerID)
			if err != nil {
				return fmt.Errorf("questionnaire answer run: parse answer id: %w", err)
			}
			pg := pgUUIDVal(parsed)
			answer = &pg
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO questionnaire_answer_run_items
				(id, tenant_id, run_id, questionnaire_id, question_id, sort_order,
				 outcome, reason_code, answer_id, error_message)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10)
		`, pgUUIDVal(uuid.New()), pgUUIDVal(tenantID), pgUUIDVal(runID), pgUUIDVal(questionnaireID),
			pgUUIDVal(questionID), sortOrder, outcome, reason, answer, boundRunMessage(message))
		if err != nil {
			return fmt.Errorf("questionnaire answer run: insert item: %w", err)
		}
		return nil
	})
}

func (s *AnswerRunStore) Recount(ctx context.Context, runID uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		_, err := tx.Exec(ctx, `
			WITH counts AS (
				SELECT
					count(*)::int AS total_count,
					count(*) FILTER (WHERE outcome = 'drafted')::int AS drafted_count,
					count(*) FILTER (WHERE outcome = 'insufficient_evidence')::int AS insufficient_count,
					count(*) FILTER (WHERE outcome = 'suppressed')::int AS suppressed_count,
					count(*) FILTER (WHERE outcome IN ('skipped_needs_mapping', 'skipped_already_answered'))::int AS skipped_count,
					count(*) FILTER (WHERE outcome = 'error')::int AS error_count
				FROM questionnaire_answer_run_items
				WHERE tenant_id = $1 AND run_id = $2
			)
			UPDATE questionnaire_answer_runs r
			SET total_count = counts.total_count,
				drafted_count = counts.drafted_count,
				insufficient_count = counts.insufficient_count,
				suppressed_count = counts.suppressed_count,
				skipped_count = counts.skipped_count,
				error_count = counts.error_count
			FROM counts
			WHERE r.tenant_id = $1 AND r.id = $2
		`, pgUUIDVal(tenantID), pgUUIDVal(runID))
		return err
	})
}

func (s *AnswerRunStore) GetDetail(ctx context.Context, runID uuid.UUID) (AnswerRunDetail, error) {
	var out AnswerRunDetail
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		row := tx.QueryRow(ctx, `
			SELECT id::text, questionnaire_id::text, status, started_by, row_cap,
				total_count, drafted_count, insufficient_count, suppressed_count,
				skipped_count, error_count, failure_reason, created_at, updated_at
			FROM questionnaire_answer_runs
			WHERE tenant_id = $1 AND id = $2
		`, pgUUIDVal(tenantID), pgUUIDVal(runID))
		if err := scanAnswerRun(row, &out.Run); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAnswerRunNotFound
			}
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id::text, run_id::text, questionnaire_id::text, question_id::text,
				sort_order, outcome, COALESCE(reason_code, ''), COALESCE(answer_id::text, ''),
				error_message, created_at, updated_at
			FROM questionnaire_answer_run_items
			WHERE tenant_id = $1 AND run_id = $2
			ORDER BY sort_order ASC, created_at ASC
		`, pgUUIDVal(tenantID), pgUUIDVal(runID))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AnswerRunItem
			if err := rows.Scan(&item.ID, &item.RunID, &item.QuestionnaireID, &item.QuestionID,
				&item.SortOrder, &item.Outcome, &item.ReasonCode, &item.AnswerID,
				&item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out.Items = append(out.Items, item)
		}
		return rows.Err()
	})
	return out, err
}

type AnswerRunService struct {
	store   *AnswerRunStore
	suggest *qaisuggest.Service
	rowCap  int
}

func NewAnswerRunService(store *AnswerRunStore, suggest *qaisuggest.Service) *AnswerRunService {
	return &AnswerRunService{store: store, suggest: suggest, rowCap: AnswerRunRowCap}
}

func (s *AnswerRunService) Start(ctx context.Context, questionnaireID uuid.UUID, startedBy string) (AnswerRunDetail, error) {
	if s.suggest == nil {
		return AnswerRunDetail{}, errors.New("questionnaire answer run: suggest service not configured")
	}
	run, err := s.store.CreateRun(ctx, questionnaireID, startedBy, s.rowCap)
	if err != nil {
		return AnswerRunDetail{}, err
	}
	runID, _ := uuid.Parse(run.ID)
	if err := s.store.MarkRunning(ctx, runID); err != nil {
		return AnswerRunDetail{}, err
	}
	questions, err := s.store.Questions(ctx, questionnaireID)
	if err != nil {
		_ = s.store.Fail(ctx, runID, err.Error())
		_ = s.store.Recount(ctx, runID)
		return s.store.GetDetail(ctx, runID)
	}
	for i, q := range questions {
		if i >= s.rowCap {
			if err := s.store.InsertItem(ctx, runID, questionnaireID, q.ID, q.SortOrder, AnswerRunOutcomeError, AnswerRunReasonRowCapExceeded, "", "row cap exceeded"); err != nil {
				_ = s.store.Fail(ctx, runID, err.Error())
				_ = s.store.Recount(ctx, runID)
				return s.store.GetDetail(ctx, runID)
			}
			continue
		}
		canceled, err := s.store.IsCanceled(ctx, runID)
		if err != nil {
			_ = s.store.Fail(ctx, runID, err.Error())
			_ = s.store.Recount(ctx, runID)
			return s.store.GetDetail(ctx, runID)
		}
		if canceled {
			_ = s.store.InsertItem(ctx, runID, questionnaireID, q.ID, q.SortOrder, AnswerRunOutcomeError, AnswerRunReasonRunCanceled, "", "run canceled before row")
			_ = s.store.Recount(ctx, runID)
			return s.store.GetDetail(ctx, runID)
		}
		outcome, reason, answerID, msg, suggestErr := s.runOne(ctx, q, startedBy)
		if ierr := s.store.InsertItem(ctx, runID, questionnaireID, q.ID, q.SortOrder, outcome, reason, answerID, msg); ierr != nil {
			_ = s.store.Fail(ctx, runID, ierr.Error())
			_ = s.store.Recount(ctx, runID)
			return s.store.GetDetail(ctx, runID)
		}
		if suggestErr != nil {
			_ = s.store.Fail(ctx, runID, suggestErr.Error())
			_ = s.store.Recount(ctx, runID)
			return s.store.GetDetail(ctx, runID)
		}
	}
	if err := s.store.Recount(ctx, runID); err != nil {
		_ = s.store.Fail(ctx, runID, err.Error())
		return s.store.GetDetail(ctx, runID)
	}
	if err := s.store.Complete(ctx, runID); err != nil {
		_ = s.store.Fail(ctx, runID, err.Error())
	}
	return s.store.GetDetail(ctx, runID)
}

func (s *AnswerRunService) runOne(ctx context.Context, q answerRunQuestion, startedBy string) (outcome, reason, answerID, message string, err error) {
	if q.NeedsMapping {
		return AnswerRunOutcomeSkippedNeedsMapping, AnswerRunReasonNeedsMapping, "", "", nil
	}
	if q.HasAnswer {
		return AnswerRunOutcomeSkippedAlreadyAnswered, AnswerRunReasonAlreadyAnswered, "", "", nil
	}
	suggestion, err := s.suggest.Suggest(ctx, qaisuggest.SuggestParams{
		QuestionID: q.ID,
		AuthoredBy: startedBy,
	})
	if err != nil {
		return AnswerRunOutcomeError, AnswerRunReasonSuggestError, "", boundRunMessage(err.Error()), err
	}
	if suggestion.InsufficientEvidence {
		return AnswerRunOutcomeInsufficientEvidence, qaisuggest.ReasonInsufficientEvidence, "", "", nil
	}
	if suggestion.Suppressed {
		return AnswerRunOutcomeSuppressed, suggestion.Reason, "", "", nil
	}
	return AnswerRunOutcomeDrafted, "", suggestion.AnswerID, "", nil
}

func (s *AnswerRunService) Get(ctx context.Context, runID uuid.UUID) (AnswerRunDetail, error) {
	return s.store.GetDetail(ctx, runID)
}

func (s *AnswerRunService) Cancel(ctx context.Context, runID uuid.UUID) (AnswerRunDetail, error) {
	if err := s.store.Cancel(ctx, runID); err != nil {
		return AnswerRunDetail{}, err
	}
	_ = s.store.Recount(ctx, runID)
	return s.store.GetDetail(ctx, runID)
}

func scanAnswerRun(row pgx.Row, out *AnswerRun) error {
	return row.Scan(&out.ID, &out.QuestionnaireID, &out.Status, &out.StartedBy, &out.RowCap,
		&out.TotalCount, &out.DraftedCount, &out.InsufficientCount, &out.SuppressedCount,
		&out.SkippedCount, &out.ErrorCount, &out.FailureReason, &out.CreatedAt, &out.UpdatedAt)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func boundRunMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

func pgUUIDVal(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
