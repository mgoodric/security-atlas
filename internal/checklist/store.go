package checklist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the slice-471 DB layer for the role-scoped checklist surface. It does
// everything under the caller's RLS context (invariant #6):
//
//  1. Reads the in-scope active controls (with the deterministic role-split
//     applied) + their citable references (scf id + linked policy ids) +
//     evidence-backing status.
//  2. Resolves a cited control/policy id to a tenant-owned row (the
//     citation-ownership gate — cross-tenant ids are RLS-invisible, AC-8).
//  3. Persists a role section + its cited items (ai_assisted, unapproved) and,
//     on a separate operator action, approves a section.
//
// Every method opens a transaction, applies the tenant GUC via
// tenancy.ApplyTenant, and runs queries inside it so RLS policies see the tenant
// id. Mirrors qaisuggest.Store / gapexplain.Store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool MUST be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is actually
// enforced — the load-bearing leg of cross-tenant isolation (AC-8).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a tenant-scoped transaction, runs fn, commits on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("checklist: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("checklist: begin tx: %w", err)
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
		return fmt.Errorf("checklist: commit: %w", err)
	}
	return nil
}

// InScopeControls loads the active control set for the caller's tenant, applies
// the DETERMINISTIC role-split (AssignRole), and attaches each control's citable
// references (SCF id + linked policy ids). This is the AC-1/AC-2 read: the split
// is computed here from owner_role + applicability_expr, never LLM-guessed.
func (s *Store) InScopeControls(ctx context.Context) ([]ControlInput, error) {
	var out []ControlInput
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListInScopeControlsForChecklist(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("checklist: list in-scope controls: %w", err)
		}
		for _, r := range rows {
			controlID := uuid.UUID(r.ID.Bytes)
			scf := ""
			if r.ScfID != nil {
				scf = *r.ScfID
			}
			role := AssignRole(r.OwnerRole, r.ApplicabilityExpr)

			// Linked policy ids (citable references for this control's tasks).
			polRows, perr := q.ListPolicyIDsLinkedToControl(ctx, dbx.ListPolicyIDsLinkedToControlParams{
				TenantID:  pgUUID(tenantID),
				ControlID: pgUUID(controlID),
			})
			if perr != nil {
				return fmt.Errorf("checklist: list linked policies: %w", perr)
			}
			polIDs := make([]string, 0, len(polRows))
			for _, p := range polRows {
				polIDs = append(polIDs, uuid.UUID(p.Bytes).String())
			}

			out = append(out, ControlInput{
				ID:          controlID.String(),
				Title:       r.Title,
				Description: r.Description,
				Role:        role,
				SCFID:       scf,
				PolicyIDs:   polIDs,
				HasEvidence: r.HasEvidence,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveControl reports whether id names a tenant-owned active control visible
// under the caller's RLS context (AC-5). A cross-tenant id is RLS-invisible, so
// this returns false for it (the AC-8 mechanism).
func (s *Store) ResolveControl(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.resolve(ctx, func(q *dbx.Queries, ctx context.Context, tenantID uuid.UUID) (bool, error) {
		_, err := q.ResolveChecklistControl(ctx, dbx.ResolveChecklistControlParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("checklist: resolve control: %w", err)
		}
		return true, nil
	})
}

// ResolvePolicy reports whether id names a tenant-owned policy visible under the
// caller's RLS context. Cross-tenant ids are RLS-invisible.
func (s *Store) ResolvePolicy(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.resolve(ctx, func(q *dbx.Queries, ctx context.Context, tenantID uuid.UUID) (bool, error) {
		_, err := q.ResolveChecklistPolicy(ctx, dbx.ResolveChecklistPolicyParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("checklist: resolve policy: %w", err)
		}
		return true, nil
	})
}

// resolve runs a tenant-scoped existence check.
func (s *Store) resolve(ctx context.Context, fn func(*dbx.Queries, context.Context, uuid.UUID) (bool, error)) (bool, error) {
	var ok bool
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		r, err := fn(q, ctx, tenantID)
		if err != nil {
			return err
		}
		ok = r
		return nil
	})
	if err != nil {
		return false, err
	}
	return ok, nil
}

// PersistSection writes one role section + its items in a single tenant tx
// (AC-7/AC-9). For the unassigned bucket aiAssisted is false + prov empty; for
// an AI section aiAssisted is true + prov populated. Every item's citations are
// the validated, tenant-resolved set (the service guarantees resolution BEFORE
// this call — P0-471-2). task_text + citations are bound as parameters (model
// output never interpolated — P0-498-7).
func (s *Store) PersistSection(ctx context.Context, generationID uuid.UUID, role Role, aiAssisted bool, prov Provenance, items []Item) (string, error) {
	var sectionID string
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		sec, err := q.InsertChecklistSection(ctx, dbx.InsertChecklistSectionParams{
			TenantID:      pgUUID(tenantID),
			GenerationID:  pgUUID(generationID),
			Role:          string(role),
			AiAssisted:    aiAssisted,
			PromptVersion: prov.PromptVersion,
			ModelName:     prov.ModelName,
			ModelVersion:  prov.ModelVersion,
			ModelProvider: prov.ModelProvider,
		})
		if err != nil {
			return fmt.Errorf("checklist: insert section: %w", err)
		}
		secID := uuid.UUID(sec.ID.Bytes)
		sectionID = secID.String()

		for i, it := range items {
			controlID, perr := uuid.Parse(it.ControlID)
			if perr != nil {
				return fmt.Errorf("checklist: parse control id: %w", perr)
			}
			citesJSON, merr := marshalCitations(it.Citations)
			if merr != nil {
				return merr
			}
			if _, err := q.InsertChecklistItem(ctx, dbx.InsertChecklistItemParams{
				TenantID:   pgUUID(tenantID),
				SectionID:  sec.ID,
				ControlID:  pgUUID(controlID),
				TaskText:   it.Task,
				Citations:  citesJSON,
				NoEvidence: it.NoEvidence,
				SortOrder:  int32(i),
			}); err != nil {
				return fmt.Errorf("checklist: insert item: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return sectionID, nil
}

// Approve flips human_approved=TRUE + records the approver on an AI-assisted,
// currently-unapproved section (AC-10). The query's WHERE (ai_assisted=TRUE AND
// human_approved=FALSE) means approving the unassigned bucket or a
// already-approved section matches no row -> ErrSectionNotFound. The DB CHECK is
// the authoritative backstop for the blank-approver shape (P0-471-6).
func (s *Store) Approve(ctx context.Context, sectionID uuid.UUID, approver string) (ApprovedSection, error) {
	var out ApprovedSection
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.ApproveChecklistSection(ctx, dbx.ApproveChecklistSectionParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(sectionID),
			HumanApprover: &approver,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSectionNotFound
			}
			return fmt.Errorf("checklist: approve section: %w", err)
		}
		approverStr := ""
		if row.HumanApprover != nil {
			approverStr = *row.HumanApprover
		}
		out = ApprovedSection{
			SectionID:     uuid.UUID(row.ID.Bytes).String(),
			Role:          Role(row.Role),
			HumanApproved: row.HumanApproved,
			HumanApprover: approverStr,
		}
		return nil
	})
	if err != nil {
		return ApprovedSection{}, err
	}
	return out, nil
}

// LoadGeneration reads back a generation's sections + their items for the
// review view (AC-9). Tenant-scoped.
func (s *Store) LoadGeneration(ctx context.Context, generationID uuid.UUID) ([]Section, error) {
	var out []Section
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		secRows, err := q.ListChecklistSectionsByGeneration(ctx, dbx.ListChecklistSectionsByGenerationParams{
			TenantID:     pgUUID(tenantID),
			GenerationID: pgUUID(generationID),
		})
		if err != nil {
			return fmt.Errorf("checklist: list sections: %w", err)
		}
		for _, sr := range secRows {
			secID := uuid.UUID(sr.ID.Bytes)
			itemRows, ierr := q.ListChecklistItemsBySection(ctx, dbx.ListChecklistItemsBySectionParams{
				TenantID:  pgUUID(tenantID),
				SectionID: sr.ID,
			})
			if ierr != nil {
				return fmt.Errorf("checklist: list items: %w", ierr)
			}
			items := make([]Item, 0, len(itemRows))
			for _, ir := range itemRows {
				var cites []Citation
				if len(ir.Citations) > 0 {
					_ = json.Unmarshal(ir.Citations, &cites)
				}
				items = append(items, Item{
					ItemID:     uuid.UUID(ir.ID.Bytes).String(),
					ControlID:  uuid.UUID(ir.ControlID.Bytes).String(),
					Task:       ir.TaskText,
					Citations:  cites,
					NoEvidence: ir.NoEvidence,
				})
			}
			approver := ""
			if sr.HumanApprover != nil {
				approver = *sr.HumanApprover
			}
			out = append(out, Section{
				SectionID:     secID.String(),
				Role:          Role(sr.Role),
				AIAssisted:    sr.AiAssisted,
				HumanApproved: sr.HumanApproved,
				HumanApprover: approver,
				Items:         items,
				ModelName:     sr.ModelName,
				ModelVersion:  sr.ModelVersion,
				ModelProvider: sr.ModelProvider,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CountSectionsForTenant returns the number of checklist sections visible to the
// caller's tenant. Used by the cross-tenant isolation integration test (AC-8).
func (s *Store) CountSectionsForTenant(ctx context.Context) (int64, error) {
	var count int64
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		c, err := q.CountChecklistSectionsForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("checklist: count sections: %w", err)
		}
		count = c
		return nil
	})
	return count, err
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// Compile-time assertions that *Store satisfies the Service's seams.
var (
	_ ControlReader   = (*Store)(nil)
	_ ControlResolver = (*Store)(nil)
	_ SectionStore    = (*Store)(nil)
)
