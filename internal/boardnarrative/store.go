package boardnarrative

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// briefAssembler is the narrow read-only view of *board.Generator the Store
// needs: assemble the deterministic Brief for a period WITHOUT persisting it.
// An interface (not the concrete type) keeps the Store testable.
type briefAssembler interface {
	Assemble(ctx context.Context, periodEnd string) (board.Brief, error)
}

// Store is the slice-440 DB layer for the board-narrative AI surface. It does
// everything under the caller's RLS context (invariant #6):
//
//  1. CoverageRollup — assembles the deterministic rollup: the board.Brief
//     numbers (via the briefAssembler) PLUS the bounded, tenant-owned citable
//     control/evidence excerpts behind them.
//  2. Resolve — resolves a cited id to a tenant-owned control/evidence row
//     (the citation-ownership gate — cross-tenant ids are RLS-invisible,
//     AC-18).
//  3. PersistDraft — persists a validated, UNAPPROVED draft section.
//  4. Approve — on a separate operator action, records the approver + final
//     text and flips human_approved=TRUE.
//
// Every method runs inside a tenant-scoped transaction (tenancy.ApplyTenant)
// so RLS sees the tenant id. Mirrors board.Store / qaisuggest.Store.
type Store struct {
	pool      *pgxpool.Pool
	assembler briefAssembler
}

// NewStore wires a Store over the application pgx pool + the board brief
// assembler. The pool MUST be the application role (NOSUPERUSER NOBYPASSRLS) so
// RLS is actually enforced — the load-bearing leg of cross-tenant isolation
// (AC-18).
func NewStore(pool *pgxpool.Pool, assembler briefAssembler) *Store {
	return &Store{pool: pool, assembler: assembler}
}

// inTx opens a tenant-scoped transaction, runs fn, commits on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("boardnarrative: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("boardnarrative: begin tx: %w", err)
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
		return fmt.Errorf("boardnarrative: commit: %w", err)
	}
	return nil
}

// CoverageRollup assembles the deterministic coverage-section rollup under the
// caller's tenant context (guardrail 1's input half). It reuses the existing
// board.Brief data path for the numbers and reads the bounded, tenant-owned
// citable control/evidence excerpts behind them. The brief is assembled OUTSIDE
// the citable-read transaction because the assembler opens its own tenant-scoped
// transactions (board.Store); both run under the same RLS context from ctx.
func (s *Store) CoverageRollup(ctx context.Context, periodEnd string) (Rollup, error) {
	brief, err := s.assembler.Assemble(ctx, periodEnd)
	if err != nil {
		return Rollup{}, err
	}
	excerpts, err := s.citableExcerpts(ctx)
	if err != nil {
		return Rollup{}, err
	}
	return RollupFromBrief(brief, excerpts)
}

// citableExcerpts reads the bounded set of tenant-owned controls (with a recent
// passing evidence record) the coverage section may cite — the controls behind
// the coverage numbers (guardrail 1's bounded cited excerpts, P0-440-8). The
// LIMIT bounds the prompt (threat-model D); the query is RLS-scoped so only
// tenant-owned rows are returned.
func (s *Store) citableExcerpts(ctx context.Context) ([]Excerpt, error) {
	var out []Excerpt
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		const q = `
			SELECT c.id::text, c.title,
			       COALESCE(c.description, '') AS descr
			FROM controls c
			WHERE c.tenant_id = $1
			  AND EXISTS (
			      SELECT 1 FROM evidence_records e
			      WHERE e.tenant_id = c.tenant_id
			        AND e.control_id = c.id
			        AND e.result = 'pass'
			  )
			ORDER BY c.title ASC, c.id ASC
			LIMIT $2`
		rows, err := tx.Query(ctx, q, pgUUID(tenantID), maxCitedExcerpts)
		if err != nil {
			return fmt.Errorf("boardnarrative: read citable controls: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, descr string
			if err := rows.Scan(&id, &title, &descr); err != nil {
				return fmt.Errorf("boardnarrative: scan control: %w", err)
			}
			out = append(out, Excerpt{
				ID:      id,
				Kind:    KindControl,
				Title:   title,
				Excerpt: boundExcerpt(descr, maxExcerptRunes),
			})
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("boardnarrative: control rows: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// maxExcerptRunes bounds each excerpt's supporting text so a long control
// description cannot blow up the prompt.
const maxExcerptRunes = 240

// boundExcerpt truncates s to at most n runes (not bytes — multibyte-safe),
// appending an ellipsis when truncated.
func boundExcerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Resolve classifies a candidate cited id by checking whether it names a
// tenant-owned control OR a tenant-owned evidence record visible under the
// caller's RLS context (guardrail 4). A cross-tenant id resolves to neither —
// the RLS-scoped queries never return another tenant's row — so Resolve returns
// ok=false for it, the mechanism behind AC-18. Mirrors qaisuggest.Store.Resolve.
func (s *Store) Resolve(ctx context.Context, id uuid.UUID) (Citation, bool, error) {
	var (
		out Citation
		ok  bool
	)
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbx.Queries, tenantID uuid.UUID) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM controls WHERE tenant_id = $1 AND id = $2)
		`, pgUUID(tenantID), pgUUID(id)).Scan(&exists); err != nil {
			return fmt.Errorf("boardnarrative: resolve control: %w", err)
		}
		if exists {
			out = Citation{Kind: KindControl, ID: id.String()}
			ok = true
			return nil
		}
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM evidence_records WHERE tenant_id = $1 AND id = $2)
		`, pgUUID(tenantID), pgUUID(id)).Scan(&exists); err != nil {
			return fmt.Errorf("boardnarrative: resolve evidence: %w", err)
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

// PersistDraft upserts the validated, UNAPPROVED draft section
// (ai_assisted=TRUE, human_approved=FALSE, human_approver=NULL) and returns the
// stored row id. The draft text + model provenance are bound as parameters
// (P0-498-7 — model output is never interpolated into SQL).
func (s *Store) PersistDraft(ctx context.Context, section SectionKey, periodEnd, rawDraft string, citationsJSON []byte, prov Provenance) (string, error) {
	var recordID string
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.UpsertBoardNarrativeDraft(ctx, dbx.UpsertBoardNarrativeDraftParams{
			TenantID:      pgUUID(tenantID),
			SectionKey:    string(section),
			PeriodEnd:     periodEnd,
			RawDraft:      rawDraft,
			Citations:     citationsJSON,
			AuthoredBy:    prov.AuthoredBy,
			PromptVersion: prov.PromptVersion,
			ModelName:     prov.ModelName,
			ModelVersion:  prov.ModelVersion,
			ModelProvider: prov.ModelProvider,
		})
		if err != nil {
			return fmt.Errorf("boardnarrative: persist draft: %w", err)
		}
		recordID = uuid.UUID(row.ID.Bytes).String()
		return nil
	})
	if err != nil {
		return "", err
	}
	return recordID, nil
}

// Approve records the operator's edited final text + approver and flips
// human_approved=TRUE on an AI-assisted draft (guardrail 2 — per section). The
// DB CHECK makes human_approved=TRUE with a blank approver impossible
// (P0-440-2); the Service rejects a blank approver before this call. Returns
// ErrRecordNotFound when the id names no tenant-owned AI-assisted draft.
func (s *Store) Approve(ctx context.Context, recordID uuid.UUID, finalText, approver string) (ApprovedSection, error) {
	var out ApprovedSection
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.ApproveBoardNarrativeSection(ctx, dbx.ApproveBoardNarrativeSectionParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(recordID),
			OperatorEdit:  finalText,
			HumanApprover: &approver,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRecordNotFound
			}
			return fmt.Errorf("boardnarrative: approve section: %w", err)
		}
		out = approvedFromRow(row)
		return nil
	})
	if err != nil {
		return ApprovedSection{}, err
	}
	return out, nil
}

// GetSection returns one section by id under the caller's tenant (used by the
// approval flow + tests).
func (s *Store) GetSection(ctx context.Context, recordID uuid.UUID) (dbx.BoardNarrativeSection, error) {
	var out dbx.BoardNarrativeSection
	err := s.inTx(ctx, func(ctx context.Context, _ pgx.Tx, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetBoardNarrativeSectionByID(ctx, dbx.GetBoardNarrativeSectionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(recordID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRecordNotFound
			}
			return fmt.Errorf("boardnarrative: get section: %w", err)
		}
		out = row
		return nil
	})
	if err != nil {
		return dbx.BoardNarrativeSection{}, err
	}
	return out, nil
}

// approvedFromRow projects a stored row into the API-shaped ApprovedSection
// (proving human_approved=TRUE + the recorded approver).
func approvedFromRow(row dbx.BoardNarrativeSection) ApprovedSection {
	approver := ""
	if row.HumanApprover != nil {
		approver = *row.HumanApprover
	}
	return ApprovedSection{
		RecordID:      uuid.UUID(row.ID.Bytes).String(),
		Section:       SectionKey(row.SectionKey),
		FinalText:     row.FinalText,
		HumanApproved: row.HumanApproved,
		HumanApprover: approver,
	}
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// Compile-time assertions that *Store satisfies the Service's seams.
var (
	_ RollupSource     = (*Store)(nil)
	_ CitationResolver = (*Store)(nil)
	_ DraftStore       = (*Store)(nil)
)
