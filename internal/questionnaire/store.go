// Slice 155 — DB layer for the questionnaire domain.
//
// The Store wraps a pgxpool.Pool and exposes high-level operations
// against the four new tables (questionnaires, questionnaire_questions,
// questionnaire_answers, answer_library). Every method opens a
// transaction, applies the tenant GUC via internal/tenancy.ApplyTenant,
// and uses the sqlc-generated CRUD where convenient — falling back to
// raw pgx for the suggestion query (decision D6).
//
// All methods are tenant-scoped via RLS (canvas invariant #6). The
// caller MUST have an `app.current_tenant` value in the request
// context; the Store does NOT bypass RLS.
package questionnaire

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the per-tenant DB facade for the questionnaire domain.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store onto the supplied pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Questionnaire is the API-shaped projection of the questionnaires row.
type Questionnaire struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	SourceLabel    string    `json:"source_label"`
	SourceFilename string    `json:"source_filename"`
	Status         string    `json:"status"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Question is the API-shaped projection of a questionnaire_questions
// row. ScfAnchorID is empty when the question is in needs_mapping state.
type Question struct {
	ID           string  `json:"id"`
	Code         string  `json:"code"`
	Text         string  `json:"text"`
	Domain       string  `json:"domain"`
	AnswerType   string  `json:"answer_type"`
	ScfAnchorID  string  `json:"scf_anchor_id"`
	SortOrder    int32   `json:"sort_order"`
	NeedsMapping bool    `json:"needs_mapping"`
	Answer       *Answer `json:"answer,omitempty"`
}

// Answer is the API-shaped projection of a questionnaire_answers row.
type Answer struct {
	ID          string `json:"id"`
	AnswerValue string `json:"answer_value"`
	Narrative   string `json:"narrative"`
	Citations   []any  `json:"citations"`
	AuthoredBy  string `json:"authored_by"`
}

// CreateQuestionnaireParams is the input for CreateQuestionnaire.
type CreateQuestionnaireParams struct {
	Name           string
	SourceLabel    string
	SourceFilename string
}

// CreateQuestionnaire inserts a fresh draft questionnaire and returns it.
func (s *Store) CreateQuestionnaire(ctx context.Context, p CreateQuestionnaireParams) (*Questionnaire, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)

	row, err := q.InsertQuestionnaire(ctx, dbx.InsertQuestionnaireParams{
		ID:             pgUUID(uuid.New().String()),
		TenantID:       pgUUID(tenant),
		Name:           p.Name,
		SourceLabel:    p.SourceLabel,
		SourceFilename: p.SourceFilename,
	})
	if err != nil {
		return nil, fmt.Errorf("questionnaire: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rowToQuestionnaire(row), nil
}

// GetQuestionnaire returns one questionnaire (the questions + answers
// are fetched separately via ListQuestionsWithAnswers).
func (s *Store) GetQuestionnaire(ctx context.Context, id string) (*Questionnaire, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	row, err := q.GetQuestionnaireByID(ctx, dbx.GetQuestionnaireByIDParams{
		TenantID: pgUUID(tenant),
		ID:       pgUUID(id),
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rowToQuestionnaire(row), nil
}

// ListQuestionnaires enumerates the tenant's questionnaires.
func (s *Store) ListQuestionnaires(ctx context.Context) ([]Questionnaire, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	rows, err := q.ListQuestionnaires(ctx, pgUUID(tenant))
	if err != nil {
		return nil, err
	}
	out := make([]Questionnaire, 0, len(rows))
	for _, r := range rows {
		out = append(out, *rowToQuestionnaire(r))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AddQuestionsFromParse appends parsed-from-Excel questions onto the
// questionnaire. Per-row scf_anchor_id is nil for unmapped rows
// (decision D5). All rows are inserted in a single transaction.
func (s *Store) AddQuestionsFromParse(ctx context.Context, qID string, parsed []ParsedQuestion) ([]Question, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	out := make([]Question, 0, len(parsed))
	for i, pq := range parsed {
		params := dbx.InsertQuestionnaireQuestionParams{
			ID:              pgUUID(uuid.New().String()),
			TenantID:        pgUUID(tenant),
			QuestionnaireID: pgUUID(qID),
			Code:            pq.Code,
			Text:            pq.Text,
			Domain:          pq.Domain,
			AnswerType:      pq.AnswerType,
			SortOrder:       int32(i),
		}
		if pq.ScfAnchorID != "" {
			params.ScfAnchorID = &pq.ScfAnchorID
		}
		row, err := q.InsertQuestionnaireQuestion(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("questionnaire: insert question %d: %w", i, err)
		}
		out = append(out, *rowToQuestion(row))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AnswerParams is the input to UpsertAnswer.
type AnswerParams struct {
	QuestionID      string
	AnswerValue     string
	Narrative       string
	Citations       []any
	AuthoredBy      string
	SaveToLibrary   bool
	SCFAnchorIDHint string // used only when SaveToLibrary is true
	SourceLabel     string // used only when SaveToLibrary is true
}

// UpsertAnswer writes a single answer (insert-or-update) and optionally
// appends a corresponding canonical entry to the AnswerLibrary.
func (s *Store) UpsertAnswer(ctx context.Context, p AnswerParams) (*Answer, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	citations, err := json.Marshal(p.Citations)
	if err != nil {
		return nil, fmt.Errorf("questionnaire: marshal citations: %w", err)
	}
	row, err := q.UpsertQuestionnaireAnswer(ctx, dbx.UpsertQuestionnaireAnswerParams{
		ID:          pgUUID(uuid.New().String()),
		TenantID:    pgUUID(tenant),
		QuestionID:  pgUUID(p.QuestionID),
		AnswerValue: p.AnswerValue,
		Narrative:   p.Narrative,
		Citations:   citations,
		AuthoredBy:  p.AuthoredBy,
	})
	if err != nil {
		return nil, fmt.Errorf("questionnaire: upsert answer: %w", err)
	}

	// Optionally save to AnswerLibrary keyed on SCF anchor.
	if p.SaveToLibrary && p.SCFAnchorIDHint != "" && p.Narrative != "" {
		_, err := q.InsertAnswerLibraryEntry(ctx, dbx.InsertAnswerLibraryEntryParams{
			ID:             pgUUID(uuid.New().String()),
			TenantID:       pgUUID(tenant),
			ScfAnchorID:    p.SCFAnchorIDHint,
			CanonicalText:  p.Narrative,
			SourceLabel:    p.SourceLabel,
			SourceAnswerID: pgUUIDFromBytes(row.ID),
		})
		if err != nil {
			return nil, fmt.Errorf("questionnaire: save to library: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	a := rowToAnswer(row)
	return a, nil
}

// ListQuestionsWithAnswers returns the full question + answer set for a
// questionnaire in stable display order.
func (s *Store) ListQuestionsWithAnswers(ctx context.Context, qID string) ([]Question, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	qrows, err := q.ListQuestionsForQuestionnaire(ctx, dbx.ListQuestionsForQuestionnaireParams{
		TenantID:        pgUUID(tenant),
		QuestionnaireID: pgUUID(qID),
	})
	if err != nil {
		return nil, err
	}
	arows, err := q.ListAnswersForQuestionnaire(ctx, dbx.ListAnswersForQuestionnaireParams{
		TenantID:        pgUUID(tenant),
		QuestionnaireID: pgUUID(qID),
	})
	if err != nil {
		return nil, err
	}
	// Map answers by question_id for stitching.
	ansByQ := make(map[string]*Answer, len(arows))
	for _, a := range arows {
		key := uuidToString(a.QuestionID)
		ansByQ[key] = rowToAnswer(a)
	}
	out := make([]Question, 0, len(qrows))
	for _, qr := range qrows {
		q := *rowToQuestion(qr)
		if a, ok := ansByQ[q.ID]; ok {
			q.Answer = a
		}
		out = append(out, q)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// SuggestForAnchorWithPool wraps SuggestForAnchor with the tenancy
// transaction so callers can use the same RLS-bound path as the rest
// of the Store.
func (s *Store) SuggestForAnchorWithPool(ctx context.Context, anchorID string, limit int) ([]Suggestion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	out, err := SuggestForAnchor(ctx, tx, anchorID, limit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// PoolTxLib is a thin adapter so a pgx.Tx satisfies LibraryReader (it
// already has Query; this is purely a documentation handle).
var _ LibraryReader = (pgx.Tx)(nil)

// ===== conversion helpers =====

func rowToQuestionnaire(r dbx.Questionnaire) *Questionnaire {
	return &Questionnaire{
		ID:             uuidToString(r.ID),
		Name:           r.Name,
		SourceLabel:    r.SourceLabel,
		SourceFilename: r.SourceFilename,
		Status:         r.Status,
		Notes:          r.Notes,
		CreatedAt:      r.CreatedAt.Time,
		UpdatedAt:      r.UpdatedAt.Time,
	}
}

func rowToQuestion(r dbx.QuestionnaireQuestion) *Question {
	q := &Question{
		ID:         uuidToString(r.ID),
		Code:       r.Code,
		Text:       r.Text,
		Domain:     r.Domain,
		AnswerType: r.AnswerType,
		SortOrder:  r.SortOrder,
	}
	if r.ScfAnchorID != nil && *r.ScfAnchorID != "" {
		q.ScfAnchorID = *r.ScfAnchorID
	} else {
		q.NeedsMapping = true
	}
	return q
}

func rowToAnswer(r dbx.QuestionnaireAnswer) *Answer {
	var citations []any
	if len(r.Citations) > 0 {
		_ = json.Unmarshal(r.Citations, &citations)
	}
	return &Answer{
		ID:          uuidToString(r.ID),
		AnswerValue: r.AnswerValue,
		Narrative:   r.Narrative,
		Citations:   citations,
		AuthoredBy:  r.AuthoredBy,
	}
}

func pgUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}

func pgUUIDFromBytes(u pgtype.UUID) pgtype.UUID {
	return u
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}
