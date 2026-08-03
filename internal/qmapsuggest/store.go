package qmapsuggest

import (
	"context"
	"encoding/json"
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

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("qmapsuggest: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("qmapsuggest: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, dbx.New(tx), tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("qmapsuggest: commit: %w", err)
	}
	return nil
}

func (s *Store) QuestionTextForMapping(ctx context.Context, questionID uuid.UUID) (string, error) {
	var text string
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		var anchor *string
		err := tx.QueryRow(ctx, `
			SELECT text, scf_anchor_id
			FROM questionnaire_questions
			WHERE tenant_id = $1 AND id = $2
		`, pgUUID(tenantID), pgUUID(questionID)).Scan(&text, &anchor)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrQuestionNotFound
		}
		if err != nil {
			return fmt.Errorf("qmapsuggest: read question: %w", err)
		}
		if anchor != nil && strings.TrimSpace(*anchor) != "" {
			return ErrQuestionCanonical
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return text, nil
}

const sqlLimit = 50

func (s *Store) RetrieveCandidates(ctx context.Context, keywords []string) ([]Candidate, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	var out []Candidate
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, _ uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id::text, a.scf_id, a.title, a.description
			FROM scf_anchors a
			JOIN framework_versions fv ON fv.id = a.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'scf'
			  AND f.tenant_id IS NULL
			  AND fv.status = 'current'
			  AND (
				  a.scf_id ILIKE ANY($1)
				  OR a.family ILIKE ANY($1)
				  OR a.title ILIKE ANY($1)
				  OR a.description ILIKE ANY($1)
			  )
			ORDER BY a.scf_id
			LIMIT $2
		`, ilikePatterns(keywords), sqlLimit)
		if err != nil {
			return fmt.Errorf("qmapsuggest: retrieve anchors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c Candidate
			var desc string
			if err := rows.Scan(&c.AnchorUUID, &c.SCFID, &c.Title, &desc); err != nil {
				return fmt.Errorf("qmapsuggest: scan anchor: %w", err)
			}
			c.Excerpt = boundExcerpt(desc, maxExcerptRune)
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("qmapsuggest: anchor rows: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ResolveAnchor(ctx context.Context, scfID string) (Candidate, bool, error) {
	var out Candidate
	var ok bool
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, _ uuid.UUID) error {
		var desc string
		err := tx.QueryRow(ctx, `
			SELECT a.id::text, a.scf_id, a.title, a.description
			FROM scf_anchors a
			JOIN framework_versions fv ON fv.id = a.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'scf'
			  AND f.tenant_id IS NULL
			  AND fv.status = 'current'
			  AND a.scf_id = $1
		`, scfID).Scan(&out.AnchorUUID, &out.SCFID, &out.Title, &desc)
		if errors.Is(err, pgx.ErrNoRows) {
			ok = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("qmapsuggest: resolve anchor: %w", err)
		}
		out.Excerpt = boundExcerpt(desc, maxExcerptRune)
		ok = true
		return nil
	})
	if err != nil {
		return Candidate{}, false, err
	}
	return out, ok, nil
}

func (s *Store) PersistProposal(ctx context.Context, questionID uuid.UUID, anchor Candidate, rationale string, candidateIDs []string, prov Provenance) (string, error) {
	candidateJSON, err := json.Marshal(candidateIDs)
	if err != nil {
		return "", fmt.Errorf("qmapsuggest: marshal candidates: %w", err)
	}
	anchorUUID, err := uuid.Parse(anchor.AnchorUUID)
	if err != nil {
		return "", fmt.Errorf("qmapsuggest: parse anchor uuid: %w", err)
	}
	var proposalID uuid.UUID
	err = s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		proposalID = uuid.New()
		err := tx.QueryRow(ctx, `
			INSERT INTO questionnaire_mapping_proposals
				(id, tenant_id, question_id, scf_anchor_uuid, scf_anchor_id,
				 scf_anchor_title, rationale, candidate_anchor_ids,
				 ai_assisted, human_approved, human_approver,
				 prompt_version, model_name, model_version, model_provider, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
			        TRUE, FALSE, NULL,
			        $9, $10, $11, $12, $13)
			RETURNING id
		`, pgUUID(proposalID), pgUUID(tenantID), pgUUID(questionID), pgUUID(anchorUUID),
			anchor.SCFID, anchor.Title, rationale, candidateJSON,
			prov.PromptVersion, prov.ModelName, prov.ModelVersion, prov.ModelProvider, prov.CreatedBy,
		).Scan(&proposalID)
		if err != nil {
			return fmt.Errorf("qmapsuggest: insert proposal: %w", err)
		}
		return s.insertAudit(ctx, tx, tenantID, &proposalID, questionID, prov.CreatedBy, "suggested", prov, map[string]any{
			"scf_anchor_id":        anchor.SCFID,
			"candidate_anchor_ids": candidateIDs,
		})
	})
	if err != nil {
		return "", err
	}
	return proposalID.String(), nil
}

func (s *Store) RecordSuppression(ctx context.Context, questionID uuid.UUID, actor, reason string, prov Provenance) error {
	return s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		return s.insertAudit(ctx, tx, tenantID, nil, questionID, actor, "suppressed", prov, map[string]any{"reason": reason})
	})
}

func (s *Store) Approve(ctx context.Context, proposalID uuid.UUID, approver string) (ApprovedProposal, error) {
	var out ApprovedProposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		var (
			questionID uuid.UUID
			scfID      string
			prov       Provenance
		)
		err := tx.QueryRow(ctx, `
			UPDATE questionnaire_mapping_proposals
			SET status = 'approved',
			    human_approved = TRUE,
			    human_approver = $3,
			    approved_at = now(),
			    updated_at = now()
			WHERE tenant_id = $1
			  AND id = $2
			  AND status = 'pending'
			  AND ai_assisted = TRUE
			RETURNING question_id, scf_anchor_id, prompt_version, model_name, model_version, model_provider
		`, pgUUID(tenantID), pgUUID(proposalID), approver).
			Scan(&questionID, &scfID, &prov.PromptVersion, &prov.ModelName, &prov.ModelVersion, &prov.ModelProvider)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProposalNotFound
		}
		if err != nil {
			return fmt.Errorf("qmapsuggest: approve proposal: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE questionnaire_questions
			SET scf_anchor_id = $3,
			    updated_at = now()
			WHERE tenant_id = $1
			  AND id = $2
			  AND scf_anchor_id IS NULL
		`, pgUUID(tenantID), pgUUID(questionID), scfID)
		if err != nil {
			return fmt.Errorf("qmapsuggest: write canonical anchor: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrQuestionCanonical
		}
		prov.CreatedBy = approver
		if err := s.insertAudit(ctx, tx, tenantID, &proposalID, questionID, approver, "approved", prov, map[string]any{"scf_anchor_id": scfID}); err != nil {
			return err
		}
		out = ApprovedProposal{
			ProposalID:    proposalID.String(),
			QuestionID:    questionID.String(),
			SCFAnchorID:   scfID,
			HumanApproved: true,
			HumanApprover: approver,
			NeedsMapping:  false,
		}
		return nil
	})
	if err != nil {
		return ApprovedProposal{}, err
	}
	return out, nil
}

func (s *Store) Reject(ctx context.Context, proposalID uuid.UUID, actor string) (RejectedProposal, error) {
	var out RejectedProposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		var (
			questionID uuid.UUID
			prov       Provenance
		)
		err := tx.QueryRow(ctx, `
			UPDATE questionnaire_mapping_proposals
			SET status = 'rejected',
			    rejected_at = now(),
			    reject_reason = 'operator_rejected',
			    updated_at = now()
			WHERE tenant_id = $1
			  AND id = $2
			  AND status = 'pending'
			RETURNING question_id, prompt_version, model_name, model_version, model_provider
		`, pgUUID(tenantID), pgUUID(proposalID)).
			Scan(&questionID, &prov.PromptVersion, &prov.ModelName, &prov.ModelVersion, &prov.ModelProvider)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProposalNotFound
		}
		if err != nil {
			return fmt.Errorf("qmapsuggest: reject proposal: %w", err)
		}
		prov.CreatedBy = actor
		if err := s.insertAudit(ctx, tx, tenantID, &proposalID, questionID, actor, "rejected", prov, map[string]any{"reason": "operator_rejected"}); err != nil {
			return err
		}
		out = RejectedProposal{ProposalID: proposalID.String(), QuestionID: questionID.String(), Status: "rejected"}
		return nil
	})
	if err != nil {
		return RejectedProposal{}, err
	}
	return out, nil
}

func (s *Store) GetProposal(ctx context.Context, proposalID uuid.UUID) (Proposal, error) {
	var out Proposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		err := tx.QueryRow(ctx, `
			SELECT id::text, question_id::text, scf_anchor_uuid::text, scf_anchor_id,
			       scf_anchor_title, rationale, prompt_version, model_name,
			       model_version, model_provider
			FROM questionnaire_mapping_proposals
			WHERE tenant_id = $1 AND id = $2
		`, pgUUID(tenantID), pgUUID(proposalID)).Scan(
			&out.ProposalID, &out.QuestionID, &out.SCFAnchorUUID, &out.SCFAnchorID,
			&out.Title, &out.Rationale, &out.PromptVersion, &out.ModelName,
			&out.ModelVersion, &out.ModelProvider,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProposalNotFound
		}
		if err != nil {
			return fmt.Errorf("qmapsuggest: get proposal: %w", err)
		}
		out.CloudRouted = isCloudProvider(out.ModelProvider)
		return nil
	})
	if err != nil {
		return Proposal{}, err
	}
	return out, nil
}

func (s *Store) CountProposals(ctx context.Context) (int, error) {
	var n int
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM questionnaire_mapping_proposals WHERE tenant_id = $1
		`, pgUUID(tenantID)).Scan(&n)
	})
	return n, err
}

func (s *Store) insertAudit(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, proposalID *uuid.UUID, questionID uuid.UUID, actor, action string, prov Provenance, payload map[string]any) error {
	payloadJSON := []byte("{}")
	if len(payload) > 0 {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("qmapsuggest: marshal audit payload: %w", err)
		}
		payloadJSON = b
	}
	var prop any
	if proposalID != nil {
		prop = pgUUID(*proposalID)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO questionnaire_mapping_proposal_audit
			(tenant_id, proposal_id, question_id, actor, action,
			 prompt_version, model_name, model_version, model_provider, payload_json)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, pgUUID(tenantID), prop, pgUUID(questionID), actor, action,
		prov.PromptVersion, prov.ModelName, prov.ModelVersion, prov.ModelProvider, payloadJSON)
	if err != nil {
		return fmt.Errorf("qmapsuggest: insert audit: %w", err)
	}
	return nil
}

func ilikePatterns(keywords []string) []string {
	pats := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(kw)
		pats = append(pats, "%"+esc+"%")
	}
	return pats
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

var (
	_ Reader        = (*Store)(nil)
	_ ProposalStore = (*Store)(nil)
)
