// pack_store.go — the quarterly-board-pack database access layer (slice 032).
//
// Like the slice-031 board.Store, every method opens a transaction, applies
// the tenant GUC via internal/tenancy, and runs queries inside that
// transaction so RLS policies see the tenant id (constitutional invariant
// 6). Reuses the inTx helper + pg* converters defined in store.go.
//
// The PackStore's write surface is `board_packs`: Insert (append a draft),
// UpdateSection (mutate a draft's content), Publish (flip draft ->
// published). The draft-only mutations are doubly guarded — the
// `board_packs` tenant_update RLS policy gates on `status = 'draft'`, the
// `UpdateBoardPackContent` / `PublishBoardPack` queries carry an explicit
// `WHERE status = 'draft'`, and a BEFORE UPDATE trigger RAISEs on a
// published row. A mutation of a published pack returns pgx.ErrNoRows, which
// this layer normalizes to ErrPackNotDraft (the HTTP layer maps it to 409).
//
// Its reads cover `board_packs` (get/list), and — reused from the slice-031
// generator's needs — `frameworks` and `risks`. The board-pack-owned
// `ListFailingEvaluations` reads `control_evaluations` as of period_end
// (decision D4).
package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// PackStore is the quarterly-board-pack database access layer over the
// application pgx pool. The pool must be connected as the application role
// (NOSUPERUSER NOBYPASSRLS) so RLS is actually enforced.
type PackStore struct {
	pool *pgxpool.Pool
}

// NewPackStore wires a PackStore over the application pgx pool.
func NewPackStore(pool *pgxpool.Pool) *PackStore {
	return &PackStore{pool: pool}
}

// ListFrameworks returns every registered framework for the tenant in ctx —
// the global catalog plus any tenant-private frameworks. Drives the pack's
// per-framework posture rows. Same query the slice-031 generator uses.
func (s *PackStore) ListFrameworks(ctx context.Context) ([]frameworkRow, error) {
	var out []frameworkRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListFrameworksForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list frameworks: %w", err)
		}
		out = make([]frameworkRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, frameworkRow{Slug: r.Slug, Name: r.Name})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListRisksAsOf returns every risk created on or before `asOf` for the
// tenant in ctx. The generator ranks these by residual severity then age.
// Reuses the slice-031 ListRisksForBoardBrief query.
func (s *PackStore) ListRisksAsOf(ctx context.Context, asOf time.Time) ([]riskRow, error) {
	var out []riskRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListRisksForBoardBrief(ctx, dbx.ListRisksForBoardBriefParams{
			TenantID:  pgUUID(tenantID),
			CreatedAt: pgTimestamptz(asOf),
		})
		if err != nil {
			return fmt.Errorf("list risks for board pack: %w", err)
		}
		out = make([]riskRow, 0, len(rows))
		for _, r := range rows {
			rr := riskRow{
				ID:            uuid.UUID(r.ID.Bytes),
				Title:         r.Title,
				Category:      string(r.Category),
				Treatment:     string(r.Treatment),
				ResidualScore: r.ResidualScore,
				InherentScore: r.InherentScore,
			}
			if r.UpdatedAt.Valid {
				rr.UpdatedAt = r.UpdatedAt.Time
			}
			out = append(out, rr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListFailingEvaluations returns the open findings for the pack — the latest
// failing control evaluation per (control, scope_cell) as of `periodEnd`
// (decision D4). Board-pack-owned read; same data semantics as slice 030's
// AuditPeriod-bound aggregator but pinned to the calendar-quarter horizon.
func (s *PackStore) ListFailingEvaluations(ctx context.Context, periodEnd time.Time) ([]Finding, error) {
	// The pack's period_end is a DATE; the control_evaluations horizon is a
	// TIMESTAMPTZ. Treat the horizon as the END of the period_end day
	// (23:59:59.999999 UTC) so an evaluation stamped anywhere on the
	// quarter-end day is in-window. Computed in Go — never a bare
	// placeholder expression (SQLSTATE 42P08).
	horizon := time.Date(periodEnd.Year(), periodEnd.Month(), periodEnd.Day(),
		23, 59, 59, 999999000, time.UTC)

	var out []Finding
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListFailingEvaluationsForPack(ctx, dbx.ListFailingEvaluationsForPackParams{
			TenantID:    pgUUID(tenantID),
			EvaluatedAt: pgTimestamptz(horizon),
		})
		if err != nil {
			return fmt.Errorf("list failing evaluations for pack: %w", err)
		}
		out = make([]Finding, 0, len(rows))
		for _, r := range rows {
			f := Finding{
				EvaluationID:    uuid.UUID(r.ID.Bytes).String(),
				ControlID:       uuid.UUID(r.ControlID.Bytes).String(),
				ScopeCellID:     uuid.UUID(r.ScopeCellID.Bytes).String(),
				FreshnessStatus: r.FreshnessStatus,
			}
			if r.EvaluatedAt.Valid {
				f.EvaluatedAt = r.EvaluatedAt.Time.UTC().Format(time.RFC3339)
			}
			out = append(out, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Insert appends one freshly generated DRAFT pack to `board_packs` and
// returns the stored row. The Pack is serialized to JSONB; the rendered
// narrative is stored alongside. The row is created in `draft` status with
// NULL publish metadata (the status-coherence CHECK enforces that).
func (s *PackStore) Insert(ctx context.Context, p Pack, narrativeMd string, generatedAt time.Time) (StoredPack, error) {
	periodEnd, err := time.Parse("2006-01-02", p.PeriodEnd)
	if err != nil {
		return StoredPack{}, fmt.Errorf("board: parse pack period_end %q: %w", p.PeriodEnd, err)
	}
	p.Status = PackStatusDraft
	contentJSON, err := json.Marshal(p)
	if err != nil {
		return StoredPack{}, fmt.Errorf("board: marshal pack content: %w", err)
	}
	id := uuid.New()

	var stored StoredPack
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.InsertBoardPack(ctx, dbx.InsertBoardPackParams{
			ID:          pgUUID(id),
			TenantID:    pgUUID(tenantID),
			PeriodEnd:   pgDate(periodEnd),
			Content:     contentJSON,
			NarrativeMd: narrativeMd,
		})
		if err != nil {
			return fmt.Errorf("insert board pack: %w", err)
		}
		stored, err = storedPackFromRow(row)
		return err
	})
	if err != nil {
		return StoredPack{}, err
	}
	return stored, nil
}

// Get fetches one pack by id for the tenant in ctx — draft or published.
// Returns ErrPackNotFound when the id does not resolve in-tenant.
func (s *PackStore) Get(ctx context.Context, id uuid.UUID) (StoredPack, error) {
	var stored StoredPack
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetBoardPackByID(ctx, dbx.GetBoardPackByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPackNotFound
		}
		if err != nil {
			return fmt.Errorf("get board pack: %w", err)
		}
		stored, err = storedPackFromRow(row)
		return err
	})
	if err != nil {
		return StoredPack{}, err
	}
	return stored, nil
}

// List returns every pack for the tenant in ctx, newest report-date first.
func (s *PackStore) List(ctx context.Context) ([]StoredPack, error) {
	var out []StoredPack
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListBoardPacks(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list board packs: %w", err)
		}
		out = make([]StoredPack, 0, len(rows))
		for _, row := range rows {
			sp, err := storedPackFromRow(row)
			if err != nil {
				return err
			}
			out = append(out, sp)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSection applies an operator edit to one section of a DRAFT pack:
// it sets the section's override text and/or approval flag and/or operator
// inputs, re-derives any computed fields (coverage delta, cost-per-point),
// re-renders the whole-pack narrative, and writes the mutated content back
// in one atomic UPDATE.
//
// The mutation runs against the live stored content — the caller passes a
// SectionEdit describing the change, NOT a whole replacement Pack, so two
// concurrent edits to different sections each apply cleanly. The
// `WHERE status = 'draft'` guard on UpdateBoardPackContent (plus the RLS
// policy + trigger) makes an edit of a published pack a zero-row no-op,
// which this method normalizes to ErrPackNotDraft.
func (s *PackStore) UpdateSection(ctx context.Context, id uuid.UUID, edit SectionEdit) (StoredPack, error) {
	if !isKnownSection(edit.SectionKey) {
		return StoredPack{}, fmt.Errorf("%w: %q", ErrUnknownSection, edit.SectionKey)
	}

	var stored StoredPack
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Read the current row inside the same tx so the read + write are
		// consistent.
		row, err := q.GetBoardPackByID(ctx, dbx.GetBoardPackByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPackNotFound
		}
		if err != nil {
			return fmt.Errorf("get board pack for update: %w", err)
		}
		if row.Status != PackStatusDraft {
			return ErrPackNotDraft
		}

		current, err := storedPackFromRow(row)
		if err != nil {
			return err
		}
		mutated := applySectionEdit(current.Content, edit)

		narrativeMd, err := RenderPackNarrative(mutated)
		if err != nil {
			return fmt.Errorf("board: re-render pack narrative: %w", err)
		}
		contentJSON, err := json.Marshal(mutated)
		if err != nil {
			return fmt.Errorf("board: marshal mutated pack content: %w", err)
		}

		updated, err := q.UpdateBoardPackContent(ctx, dbx.UpdateBoardPackContentParams{
			TenantID:    pgUUID(tenantID),
			ID:          pgUUID(id),
			Content:     contentJSON,
			NarrativeMd: narrativeMd,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// RLS / WHERE status='draft' filtered the row — the pack was
			// published between the read and the write.
			return ErrPackNotDraft
		}
		if err != nil {
			return fmt.Errorf("update board pack content: %w", err)
		}
		stored, err = storedPackFromRow(updated)
		return err
	})
	if err != nil {
		return StoredPack{}, err
	}
	return stored, nil
}

// Publish flips a DRAFT pack to PUBLISHED — but only when EVERY fixed
// section is approved (decision D6, AC-5). It re-renders the final narrative
// from the frozen content and writes the publish metadata in the same
// atomic UPDATE that flips the status.
//
// Rejections:
//   - ErrPackNotFound  — id does not resolve in-tenant
//   - ErrPackNotDraft  — the pack is already published
//   - ErrPackNotReady  — at least one section is not approved
func (s *PackStore) Publish(ctx context.Context, id uuid.UUID, publishedBy string) (StoredPack, error) {
	publishedAt := time.Now().UTC()

	var stored StoredPack
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetBoardPackByID(ctx, dbx.GetBoardPackByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPackNotFound
		}
		if err != nil {
			return fmt.Errorf("get board pack for publish: %w", err)
		}
		if row.Status != PackStatusDraft {
			return ErrPackNotDraft
		}

		current, err := storedPackFromRow(row)
		if err != nil {
			return err
		}
		// D6 publish gate: every section must be approved.
		if title, ok := allSectionsApproved(current.Content); !ok {
			return fmt.Errorf("%w (first unapproved: %s)", ErrPackNotReady, title)
		}

		frozen := current.Content
		frozen.Status = PackStatusPublished
		narrativeMd, err := RenderPackNarrative(frozen)
		if err != nil {
			return fmt.Errorf("board: render published pack narrative: %w", err)
		}
		contentJSON, err := json.Marshal(frozen)
		if err != nil {
			return fmt.Errorf("board: marshal published pack content: %w", err)
		}

		pubBy := publishedBy
		updated, err := q.PublishBoardPack(ctx, dbx.PublishBoardPackParams{
			TenantID:    pgUUID(tenantID),
			ID:          pgUUID(id),
			Content:     contentJSON,
			NarrativeMd: narrativeMd,
			PublishedBy: &pubBy,
			PublishedAt: pgTimestamptz(publishedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPackNotDraft
		}
		if err != nil {
			return fmt.Errorf("publish board pack: %w", err)
		}
		stored, err = storedPackFromRow(updated)
		return err
	})
	if err != nil {
		return StoredPack{}, err
	}
	return stored, nil
}

// SectionEdit describes one operator edit to one section of a draft pack.
// Every field is optional — only the populated ones are applied:
//
//   - OverrideText (when the pointer is non-nil) replaces the section's
//     override narrative. An empty string clears the override (the
//     templated text takes over again — AC-2).
//   - Approved (when non-nil) sets the per-section approval flag (AC-5).
//   - Inputs (when non-nil) carries operator-entered structured inputs for
//     the operational_metrics / investment / coverage_trend sections
//     (decisions D3 + D5).
type SectionEdit struct {
	SectionKey   string
	OverrideText *string
	Approved     *bool
	Inputs       *SectionInputs
}

// SectionInputs is the operator-entered structured payload for a section.
// All fields are pointers so "not supplied" is distinct from "set to zero"
// — an operator setting incident_count to 0 is meaningful (a clean quarter).
type SectionInputs struct {
	// operational_metrics (decision D3).
	PhishingPassRatePct *int `json:"phishing_pass_rate_pct,omitempty"`
	P1PatchMedianDays   *int `json:"p1_patch_median_days,omitempty"`
	IncidentCount       *int `json:"incident_count,omitempty"`
	VendorReviewsOnTime *int `json:"vendor_reviews_on_time,omitempty"`
	VendorReviewsTotal  *int `json:"vendor_reviews_total,omitempty"`

	// investment + coverage_trend (decision D5).
	SpendUSD            *int `json:"spend_usd,omitempty"`
	BaselineCoveragePct *int `json:"baseline_coverage_pct,omitempty"`
}

// applySectionEdit returns a copy of `p` with `edit` applied to its target
// section, re-deriving the computed fields (coverage delta,
// cost-per-coverage-point) and re-rendering that section's templated text.
// Pure — does not touch the DB.
func applySectionEdit(p Pack, edit SectionEdit) Pack {
	// Shallow-copy the section map so the input Pack is not mutated.
	sections := make(map[string]Section, len(p.Sections))
	for k, v := range p.Sections {
		sections[k] = v
	}
	out := p
	out.Sections = sections

	sec := sections[edit.SectionKey]

	if edit.OverrideText != nil {
		sec.OverrideText = *edit.OverrideText
	}
	if edit.Approved != nil {
		sec.Approved = *edit.Approved
	}
	if edit.Inputs != nil {
		applyInputs(&sec, edit.Inputs)
	}

	// Re-derive computed fields and keep coverage_trend / investment in sync.
	sections[edit.SectionKey] = sec
	recomputeDerived(sections)

	// Re-render the templated narrative for the edited section AND any
	// section whose derived fields changed (coverage_trend + investment are
	// coupled). Cheap — pure text/template.
	for _, key := range []string{edit.SectionKey, SectionCoverageTrend, SectionInvestment} {
		s := sections[key]
		if text, err := renderSectionNarrative(s, out.PeriodEnd); err == nil {
			s.TemplatedText = text
			sections[key] = s
		}
	}
	return out
}

// applyInputs writes the operator-entered inputs onto a section's Data.
func applyInputs(sec *Section, in *SectionInputs) {
	d := sec.Data
	if in.PhishingPassRatePct != nil {
		d.PhishingPassRatePct = in.PhishingPassRatePct
	}
	if in.P1PatchMedianDays != nil {
		d.P1PatchMedianDays = in.P1PatchMedianDays
	}
	if in.IncidentCount != nil {
		d.IncidentCount = in.IncidentCount
	}
	if in.VendorReviewsOnTime != nil {
		d.VendorReviewsOnTime = in.VendorReviewsOnTime
	}
	if in.VendorReviewsTotal != nil {
		d.VendorReviewsTotal = in.VendorReviewsTotal
	}
	if in.SpendUSD != nil {
		d.SpendUSD = *in.SpendUSD
	}
	if in.BaselineCoveragePct != nil {
		d.BaselineCoveragePct = *in.BaselineCoveragePct
	}
	sec.Data = d
}

// recomputeDerived keeps the coverage_trend and investment sections'
// computed fields consistent after an edit (decision D5):
//
//   - coverage_trend.coverage_delta = coverage_pct - baseline_coverage_pct
//   - investment.coverage_delta mirrors coverage_trend's delta
//   - investment.cost_per_coverage_point = spend / max(delta, 1)
func recomputeDerived(sections map[string]Section) {
	trend, hasTrend := sections[SectionCoverageTrend]
	inv, hasInv := sections[SectionInvestment]
	if !hasTrend || !hasInv {
		return
	}
	delta := trend.Data.CoveragePct - trend.Data.BaselineCoveragePct
	trend.Data.CoverageDelta = delta
	inv.Data.CoverageDelta = delta
	inv.Data.CostPerCoveragePoint = costPerCoveragePoint(inv.Data.SpendUSD, delta)
	sections[SectionCoverageTrend] = trend
	sections[SectionInvestment] = inv
}

// storedPackFromRow deserializes a `board_packs` row into a StoredPack. The
// stored `content` JSONB is unmarshalled back into the Pack struct.
func storedPackFromRow(row dbx.BoardPack) (StoredPack, error) {
	var content Pack
	if err := json.Unmarshal(row.Content, &content); err != nil {
		return StoredPack{}, fmt.Errorf("board: unmarshal pack content: %w", err)
	}
	sp := StoredPack{
		ID:          uuid.UUID(row.ID.Bytes),
		Status:      row.Status,
		Content:     content,
		NarrativeMd: row.NarrativeMd,
	}
	if row.PeriodEnd.Valid {
		sp.PeriodEnd = row.PeriodEnd.Time.Format("2006-01-02")
	}
	if row.PublishedBy != nil {
		sp.PublishedBy = *row.PublishedBy
	}
	if row.PublishedAt.Valid {
		sp.PublishedAt = row.PublishedAt.Time
	}
	if row.CreatedAt.Valid {
		sp.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		sp.UpdatedAt = row.UpdatedAt.Time
	}
	return sp, nil
}

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors board.Store.inTx (store.go) — kept as a separate method
// on PackStore so the two stores stay independently testable.
func (s *PackStore) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("board: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("board: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("board: commit: %w", err)
	}
	return nil
}
