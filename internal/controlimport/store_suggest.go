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

// readCandidates reads a batch's candidates keyed by row ID, in rank order.
func readCandidates(ctx context.Context, tx pgx.Tx, batchID string) (map[string][]Candidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT row_id::text, rank, scf_anchor_id::text, scf_id, title, source, reaches, strength,
		       ai_assisted, COALESCE(rationale, '')
		FROM control_import_candidates
		WHERE batch_id = $1::uuid
		ORDER BY row_id, rank
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("controlimport: read candidates: %w", err)
	}
	defer rows.Close()
	out := map[string][]Candidate{}
	for rows.Next() {
		var rowID string
		var c Candidate
		var reaches []byte
		if err := rows.Scan(&rowID, &c.Rank, &c.AnchorID, &c.SCFID, &c.Title, &c.Source, &reaches, &c.Strength,
			&c.AIAssisted, &c.Rationale); err != nil {
			return nil, fmt.Errorf("controlimport: scan candidate: %w", err)
		}
		if err := json.Unmarshal(reaches, &c.Reaches); err != nil {
			return nil, fmt.Errorf("controlimport: decode reaches: %w", err)
		}
		c.Reaches = nonNilReqs(c.Reaches)
		out[rowID] = append(out[rowID], c)
	}
	return out, rows.Err()
}

// ListRows reads a batch's staged rows with their candidates. Another
// tenant's batch reads as no rows under RLS.
func (s *Store) ListRows(ctx context.Context, batchID string, f RowFilter) ([]BatchControl, error) {
	if _, err := uuid.Parse(batchID); err != nil {
		return nil, ErrBatchNotFound
	}
	var out []BatchControl
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		var err error
		out, err = readStaged(ctx, tx, batchID, f)
		return err
	})
	return out, err
}

// ResolveSCFRelease returns the current release of the global SCF catalog.
func (s *Store) ResolveSCFRelease(ctx context.Context) (SCFRelease, bool, error) {
	var out SCFRelease
	var ok bool
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		err := tx.QueryRow(ctx, `
			SELECT fv.id::text, fv.version
			FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.status = 'current'
			ORDER BY fv.created_at DESC
			LIMIT 1
		`).Scan(&out.VersionID, &out.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("controlimport: read SCF release: %w", err)
		}
		ok = true
		return nil
	})
	return out, ok, err
}

// CrosswalkAnchors reads the given requirements' mappings to anchors of one
// SCF release, through the pinned requirement-to-anchors query the coverage
// API uses (effective_fw_to_scf_edges; no_relationship and rejected edges
// excluded).
func (s *Store) CrosswalkAnchors(ctx context.Context, scfVersionID string, requirementIDs []string) ([]CrosswalkEdge, error) {
	if len(requirementIDs) == 0 {
		return nil, nil
	}
	versionID, err := uuid.Parse(scfVersionID)
	if err != nil {
		return nil, fmt.Errorf("controlimport: SCF version id: %w", err)
	}
	var out []CrosswalkEdge
	err = s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		q := dbx.New(tx)
		for _, reqID := range requirementIDs {
			rid, err := uuid.Parse(reqID)
			if err != nil {
				return fmt.Errorf("controlimport: requirement id: %w", err)
			}
			rows, err := q.ListAnchorsForRequirementWithEdgesByFrameworkVersion(ctx, dbx.ListAnchorsForRequirementWithEdgesByFrameworkVersionParams{
				FrameworkRequirementID: pgtype.UUID{Bytes: rid, Valid: true},
				FrameworkVersionID:     pgtype.UUID{Bytes: versionID, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("controlimport: read crosswalk for %s: %w", reqID, err)
			}
			for _, r := range rows {
				out = append(out, CrosswalkEdge{
					RequirementID: reqID,
					AnchorID:      uuid.UUID(r.ScfAnchorID.Bytes).String(),
					SCFID:         r.ScfID,
					Title:         r.AnchorTitle,
					Strength:      r.Strength,
				})
			}
		}
		return nil
	})
	return out, err
}

// KeywordAnchors runs the question-mapping catalog keyword retrieval against
// one SCF release, the batch's, so keyword and crosswalk candidates come
// from the same release.
func (s *Store) KeywordAnchors(ctx context.Context, scfVersionID string, keywords []string) ([]AnchorCandidate, error) {
	cands, err := s.keyword.RetrieveCandidatesInRelease(ctx, scfVersionID, keywords)
	if err != nil {
		return nil, err
	}
	out := make([]AnchorCandidate, len(cands))
	for i, c := range cands {
		out[i] = AnchorCandidate{AnchorID: c.AnchorUUID, SCFID: c.SCFID, Title: c.Title, Description: c.Excerpt}
	}
	return out, nil
}

// SaveSuggestions replaces each row's candidates and flag and sets the
// batch's suggestions status, in one transaction.
func (s *Store) SaveSuggestions(ctx context.Context, batchID string, rows []RowSuggestions, status string) error {
	return s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
		for _, r := range rows {
			if _, err := tx.Exec(ctx, `DELETE FROM control_import_candidates WHERE row_id = $1::uuid`, r.RowID); err != nil {
				return fmt.Errorf("controlimport: clear candidates: %w", err)
			}
			for _, c := range r.Candidates {
				reaches, err := json.Marshal(nonNilReqs(c.Reaches))
				if err != nil {
					return fmt.Errorf("controlimport: encode reaches: %w", err)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO control_import_candidates (
						tenant_id, batch_id, row_id, rank, scf_anchor_id, scf_id, title,
						source, reaches, strength, ai_assisted, rationale)
					VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8, $9::jsonb, $10,
						$11, NULLIF($12, ''))
				`, tenantID, batchID, r.RowID, c.Rank, c.AnchorID, c.SCFID, c.Title,
					c.Source, string(reaches), c.Strength, c.AIAssisted, c.Rationale); err != nil {
					return fmt.Errorf("controlimport: insert candidate: %w", err)
				}
			}
			if _, err := tx.Exec(ctx, `
				UPDATE control_import_rows
				SET no_candidate_reaches = $2, llm_rerank = $3, updated_at = now()
				WHERE id = $1::uuid
			`, r.RowID, r.NoCandidateReaches, rerankState(r.LLMRerank)); err != nil {
				return fmt.Errorf("controlimport: flag row: %w", err)
			}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE control_import_batches
			SET suggestions_status = $2, updated_at = now()
			WHERE id = $1::uuid
		`, batchID, status)
		if err != nil {
			return fmt.Errorf("controlimport: set suggestions status: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrBatchNotFound
		}
		return nil
	})
}

func rerankState(s string) string {
	if s == "" {
		return RerankOff
	}
	return s
}

func releaseVersionID(r *SCFRelease) *string {
	if r == nil {
		return nil
	}
	return &r.VersionID
}

func releaseVersion(r *SCFRelease) *string {
	if r == nil {
		return nil
	}
	return &r.Version
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonNilCandidates(s []Candidate) []Candidate {
	if s == nil {
		return []Candidate{}
	}
	return s
}
