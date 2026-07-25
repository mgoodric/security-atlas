package qaisuggest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the slice-441 DB layer for the questionnaire AI-suggestion surface.
// It does four things, ALL under the caller's RLS context (invariant #6):
//
//  1. Reads the question text (and proves the question is tenant-owned).
//  2. Retrieves keyword-matched policy + evidence candidates owned by the
//     tenant (the keyword first-pass — P0-441-5, NO pgvector).
//  3. Resolves a cited id to a tenant-owned policy/evidence row (the
//     citation-ownership gate — cross-tenant ids are RLS-invisible, AC-16).
//  4. Persists the AI-suggested DRAFT (ai_assisted, unapproved) and, on a
//     separate operator action, approves it.
//
// Every method opens a transaction, applies the tenant GUC via
// internal/tenancy.ApplyTenant, and runs queries inside it so RLS policies see
// the tenant id. Mirrors questionnaire.Store / gapexplain.Store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool MUST be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced — that is the load-bearing leg of cross-tenant isolation
// (AC-16).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a tenant-scoped transaction, runs fn, commits on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("qaisuggest: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("qaisuggest: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, tx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("qaisuggest: commit: %w", err)
	}
	return nil
}

// QuestionText returns one questionnaire question's text under the caller's
// tenant context (AC-1: proves the question is tenant-owned before any
// retrieval). A cross-tenant or absent id yields ErrQuestionNotFound.
func (s *Store) QuestionText(ctx context.Context, questionID uuid.UUID) (string, error) {
	var text string
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		row := tx.QueryRow(ctx, `
			SELECT text FROM questionnaire_questions
			WHERE tenant_id = $1 AND id = $2
		`, pgUUID(tenantID), pgUUID(questionID))
		if err := row.Scan(&text); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrQuestionNotFound
			}
			return fmt.Errorf("qaisuggest: read question: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return text, nil
}

// RetrieveCandidates runs the keyword first-pass: it returns tenant-owned
// policy + evidence candidates whose text matches ANY of the given keywords
// (AC-1). The SQL casts a wide net (any-token ILIKE under RLS); the caller
// (Service) ranks + bounds the result in-memory via rankCandidates. Returns an
// empty slice (not an error) when nothing matches — that is the
// insufficient-evidence precondition (AC-5).
//
// Policy candidates: published/approved policies whose title or body matches.
// Evidence candidates: evidence records whose CONTROL title matches (evidence
// has no free text of its own; it is "about" its control). Both are capped at
// the SQL level by sqlLimit so a tenant with a huge corpus cannot blow up the
// in-memory rank.
func (s *Store) RetrieveCandidates(ctx context.Context, keywords []string) ([]Candidate, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	var out []Candidate
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		pol, err := retrievePolicies(ctx, tx, tenantID, keywords)
		if err != nil {
			return err
		}
		ev, err := retrieveEvidence(ctx, tx, tenantID, keywords)
		if err != nil {
			return err
		}
		out = append(out, pol...)
		out = append(out, ev...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sqlLimit caps each candidate-source query at the SQL layer. Generous enough
// to surface the real matches, bounded so a large corpus cannot flood the
// in-memory rank.
const sqlLimit = 50

// ilikePatterns builds the `%kw%` ILIKE patterns + an `ANY($n)` array arg.
func ilikePatterns(keywords []string) []string {
	pats := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		// Escape ILIKE metacharacters so a keyword with % or _ matches
		// literally (defense — keywords are tokenized alnum so this is belt).
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(kw)
		pats = append(pats, "%"+esc+"%")
	}
	return pats
}

// retrievePolicies returns tenant-owned policy candidates matching any keyword
// in title or body_md. Only published/approved policies are citable — a draft
// policy is not something to assert to a customer.
func retrievePolicies(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, keywords []string) ([]Candidate, error) {
	const q = `
		SELECT id::text, title, body_md
		FROM policies
		WHERE tenant_id = $1
		  AND status IN ('approved', 'published')
		  AND (title ILIKE ANY($2) OR body_md ILIKE ANY($2))
		ORDER BY updated_at DESC, id ASC
		LIMIT $3`
	rows, err := tx.Query(ctx, q, pgUUID(tenantID), ilikePatterns(keywords), sqlLimit)
	if err != nil {
		return nil, fmt.Errorf("qaisuggest: retrieve policies: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var id, title, body string
		if err := rows.Scan(&id, &title, &body); err != nil {
			return nil, fmt.Errorf("qaisuggest: scan policy: %w", err)
		}
		out = append(out, Candidate{
			ID:      id,
			Kind:    KindPolicy,
			Title:   title,
			Excerpt: boundExcerpt(body, maxExcerptRunes),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qaisuggest: policy rows: %w", err)
	}
	return out, nil
}

// retrieveEvidence returns tenant-owned evidence candidates whose control's
// title matches any keyword. Evidence is cited by its own id but described by
// its control (evidence has no free-text title of its own). Only passing
// evidence is surfaced as support for a positive answer.
func retrieveEvidence(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, keywords []string) ([]Candidate, error) {
	const q = `
		SELECT e.id::text, c.title, e.result::text, e.evidence_kind
		FROM evidence_records e
		JOIN controls c ON c.tenant_id = e.tenant_id AND c.id = e.control_id
		WHERE e.tenant_id = $1
		  AND e.result = 'pass'
		  AND c.title ILIKE ANY($2)
		ORDER BY e.observed_at DESC, e.id ASC
		LIMIT $3`
	rows, err := tx.Query(ctx, q, pgUUID(tenantID), ilikePatterns(keywords), sqlLimit)
	if err != nil {
		return nil, fmt.Errorf("qaisuggest: retrieve evidence: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var id, title, result string
		var kind *string
		if err := rows.Scan(&id, &title, &result, &kind); err != nil {
			return nil, fmt.Errorf("qaisuggest: scan evidence: %w", err)
		}
		k := ""
		if kind != nil {
			k = *kind
		}
		excerpt := fmt.Sprintf("evidence for control %q: result=%s", oneLine(title), result)
		if k != "" {
			excerpt += " kind=" + k
		}
		out = append(out, Candidate{
			ID:      id,
			Kind:    KindEvidence,
			Title:   title,
			Excerpt: excerpt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qaisuggest: evidence rows: %w", err)
	}
	return out, nil
}

// Resolve classifies a candidate cited id by checking whether it names a
// tenant-owned policy OR a tenant-owned evidence record visible under the
// caller's RLS context (AC-4). A cross-tenant id resolves to neither — the
// RLS-scoped queries never return another tenant's row — so Resolve returns
// ok=false for it, which is the mechanism behind AC-16.
func (s *Store) Resolve(ctx context.Context, id uuid.UUID) (Citation, bool, error) {
	var (
		out Citation
		ok  bool
	)
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM policies WHERE tenant_id = $1 AND id = $2)
		`, pgUUID(tenantID), pgUUID(id)).Scan(&exists); err != nil {
			return fmt.Errorf("qaisuggest: resolve policy: %w", err)
		}
		if exists {
			out = Citation{Kind: KindPolicy, ID: id.String()}
			ok = true
			return nil
		}
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM evidence_records WHERE tenant_id = $1 AND id = $2)
		`, pgUUID(tenantID), pgUUID(id)).Scan(&exists); err != nil {
			return fmt.Errorf("qaisuggest: resolve evidence: %w", err)
		}
		if exists {
			out = Citation{Kind: KindEvidence, ID: id.String()}
			ok = true
			return nil
		}
		ok = false
		return nil
	})
	if err != nil {
		return Citation{}, false, err
	}
	return out, ok, nil
}

// PersistDraft upserts the AI-suggested DRAFT answer for one question
// (ai_assisted=TRUE, human_approved=FALSE, human_approver=NULL) and returns the
// stored row id. The draft text + model provenance are bound as parameters
// (P0-498-7 — model output is never interpolated into SQL). citationsJSON is
// the JSONB array of resolved citations the operator will see.
func (s *Store) PersistDraft(ctx context.Context, questionID uuid.UUID, narrative string, citationsJSON []byte, prov Provenance) (string, error) {
	var answerID string
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.UpsertAISuggestedAnswer(ctx, dbx.UpsertAISuggestedAnswerParams{
			ID:            pgUUID(uuid.New()),
			TenantID:      pgUUID(tenantID),
			QuestionID:    pgUUID(questionID),
			AnswerValue:   "",
			Narrative:     narrative,
			Citations:     citationsJSON,
			AuthoredBy:    prov.AuthoredBy,
			PromptVersion: prov.PromptVersion,
			ModelName:     prov.ModelName,
			ModelVersion:  prov.ModelVersion,
			ModelProvider: prov.ModelProvider,
		})
		if err != nil {
			return fmt.Errorf("qaisuggest: persist draft: %w", err)
		}
		answerID = uuid.UUID(row.ID.Bytes).String()
		return nil
	})
	if err != nil {
		return "", err
	}
	return answerID, nil
}

// Provenance is the model + author metadata persisted onto a draft.
type Provenance struct {
	AuthoredBy    string
	PromptVersion string
	ModelName     string
	ModelVersion  string
	ModelProvider string
}

// ApproveDraft sets human_approved=TRUE + records the approver on an
// AI-assisted draft, optionally replacing the narrative with the operator's
// edited final text (AC-6/AC-12). The DB CHECK makes human_approved=TRUE with a
// blank approver impossible (P0-441-8); the Service rejects a blank approver
// before this call. Returns ErrAnswerNotFound when the id names no tenant-owned
// AI-assisted draft.
func (s *Store) ApproveDraft(ctx context.Context, answerID uuid.UUID, finalNarrative, answerValue, approver string) (dbx.QuestionnaireAnswer, error) {
	var out dbx.QuestionnaireAnswer
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.ApproveQuestionnaireAnswer(ctx, dbx.ApproveQuestionnaireAnswerParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(answerID),
			Narrative:     finalNarrative,
			AnswerValue:   answerValue,
			HumanApprover: &approver,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAnswerNotFound
			}
			return fmt.Errorf("qaisuggest: approve draft: %w", err)
		}
		out = row
		return nil
	})
	if err != nil {
		return dbx.QuestionnaireAnswer{}, err
	}
	return out, nil
}

// GetAnswer returns one answer by id under the caller's tenant (used by the
// approval flow + tests).
func (s *Store) GetAnswer(ctx context.Context, answerID uuid.UUID) (dbx.QuestionnaireAnswer, error) {
	var out dbx.QuestionnaireAnswer
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetQuestionnaireAnswerByID(ctx, dbx.GetQuestionnaireAnswerByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(answerID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAnswerNotFound
			}
			return fmt.Errorf("qaisuggest: get answer: %w", err)
		}
		out = row
		return nil
	})
	if err != nil {
		return dbx.QuestionnaireAnswer{}, err
	}
	return out, nil
}

// Approve is the ApprovalStore-shaped wrapper over ApproveDraft: it approves
// the draft and projects the stored row into the API-shaped ApprovedAnswer
// (proving human_approved=TRUE + the recorded approver).
func (s *Store) Approve(ctx context.Context, answerID uuid.UUID, finalNarrative, answerValue, approver string) (ApprovedAnswer, error) {
	row, err := s.ApproveDraft(ctx, answerID, finalNarrative, answerValue, approver)
	if err != nil {
		return ApprovedAnswer{}, err
	}
	approverStr := ""
	if row.HumanApprover != nil {
		approverStr = *row.HumanApprover
	}
	return ApprovedAnswer{
		AnswerID:      uuid.UUID(row.ID.Bytes).String(),
		QuestionID:    uuid.UUID(row.QuestionID.Bytes).String(),
		Narrative:     row.Narrative,
		AnswerValue:   row.AnswerValue,
		HumanApproved: row.HumanApproved,
		HumanApprover: approverStr,
	}, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// Compile-time assertions that *Store satisfies the Service's seams.
var (
	_ Retriever        = (*Store)(nil)
	_ CitationResolver = (*Store)(nil)
	_ ApprovalStore    = (*Store)(nil)
)
