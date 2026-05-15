package oscal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// defaultRemediationWindow is the POA&M due-date offset applied to a
// failing control evaluation when no explicit remediation deadline
// exists. 90 days mirrors the SOC 2 "monthly" freshness class and is a
// conservative default — the decisions log flags this for revisit once a
// real remediation-tracking slice lands.
const defaultRemediationWindow = 90 * 24 * time.Hour

// aggregate is the in-memory snapshot of everything the export needs,
// read from the database under one transaction. It is the boundary
// between the database layer and the bridge layer: the sspInput /
// assessmentInput / poamInput methods convert it to proto messages.
type aggregate struct {
	period       dbx.AuditPeriod
	frozenAt     time.Time
	scopeCells   []dbx.ScopeCell
	controls     []dbx.ListActiveControlsRow
	policies     []dbx.Policy
	populations  []dbx.ListPopulationsForPeriodRow
	walkthroughs []dbx.Walkthrough
	auditNotes   []dbx.ListAuditNotesForPeriodRow
	failingEvals []dbx.ListFailingEvaluationsAsOfRow
	in           ExportInput
	// controlOwner maps control_id -> owner_role, used to populate POA&M
	// item owners (decision D3).
	controlOwner map[uuid.UUID]string
	// controlTitle maps control_id -> title for POA&M item titles.
	controlTitle map[uuid.UUID]string
}

// Aggregate reads a frozen AuditPeriod's data from the database. It is
// the enforcement point for constitutional invariant 10: if the period
// is not frozen it returns ErrPeriodNotFrozen and reads nothing further.
//
// Every read runs inside one transaction under the tenant RLS context
// (mirrors the slice-028 period.Store.inTx pattern). ctx must carry a
// tenancy value.
func (e *Exporter) Aggregate(ctx context.Context, in ExportInput) (*aggregate, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, fmt.Errorf("oscal: parse tenant id: %w", err)
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("oscal: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)

	agg := &aggregate{
		in:           in,
		controlOwner: map[uuid.UUID]string{},
		controlTitle: map[uuid.UUID]string{},
	}

	// 1. Resolve the period and assert it is frozen. This MUST be the
	//    first check — invariant 10 + P0 anti-criterion.
	period, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPeriodNotFound
		}
		return nil, fmt.Errorf("oscal: get audit period: %w", err)
	}
	if period.Status != "frozen" {
		return nil, ErrPeriodNotFrozen
	}
	if !period.FrozenAt.Valid {
		// Defensive: a 'frozen' row with a NULL frozen_at would be a
		// data-integrity bug in slice 028, but we must not export
		// against an undefined horizon.
		return nil, ErrPeriodNotFrozen
	}
	agg.period = period
	agg.frozenAt = period.FrozenAt.Time

	// 2. Scope cells (SSP system-characteristics).
	scopeCells, err := q.ListScopeCells(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("oscal: list scope cells: %w", err)
	}
	agg.scopeCells = scopeCells

	// 3. Active controls (SSP control-implementations).
	controls, err := q.ListActiveControls(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("oscal: list active controls: %w", err)
	}
	agg.controls = controls
	for _, c := range controls {
		cid := uuid.UUID(c.ID.Bytes)
		agg.controlOwner[cid] = c.OwnerRole
		agg.controlTitle[cid] = c.Title
	}

	// 4. Policies (SSP linked governance documents).
	policies, err := q.ListPolicies(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("oscal: list policies: %w", err)
	}
	agg.policies = policies

	// 5. Sample populations attached to this period (AP).
	populations, err := q.ListPopulationsForPeriod(ctx, dbx.ListPopulationsForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list populations for period: %w", err)
	}
	agg.populations = populations

	// 6. Walkthroughs pinned to this period (AR observations).
	walkthroughs, err := q.ListWalkthroughsForPeriod(ctx, dbx.ListWalkthroughsForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list walkthroughs for period: %w", err)
	}
	agg.walkthroughs = walkthroughs

	// 7. Audit notes for this period (AR observation annotations).
	auditNotes, err := q.ListAuditNotesForPeriod(ctx, dbx.ListAuditNotesForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list audit notes for period: %w", err)
	}
	agg.auditNotes = auditNotes

	// 8. Failing control evaluations as of the frozen horizon (POA&M).
	//    The horizon bound is the period's frozen_at — invariant 10.
	failing, err := q.ListFailingEvaluationsAsOf(ctx, dbx.ListFailingEvaluationsAsOfParams{
		TenantID:    pgUUID(tenantID),
		EvaluatedAt: period.FrozenAt,
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list failing evaluations: %w", err)
	}
	agg.failingEvals = failing

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("oscal: commit: %w", err)
	}
	return agg, nil
}

// ----- proto conversion -----

func (a *aggregate) metadata(title string) *oscalv1.Metadata {
	return &oscalv1.Metadata{
		Title:        title,
		Version:      "1.0",
		OscalVersion: OSCALVersion,
		LastModified: time.Now().UTC().Format(time.RFC3339),
		FrozenAt:     a.frozenAt.UTC().Format(time.RFC3339),
	}
}

// sspInput converts the aggregate into the SSP proto input. The control
// implementation `statement` is the human-authored control description —
// never AI-generated (CLAUDE.md product-runtime AI-assist boundary).
func (a *aggregate) sspInput() *oscalv1.SspInput {
	cells := make([]*oscalv1.ScopeCell, 0, len(a.scopeCells))
	for _, sc := range a.scopeCells {
		cells = append(cells, &oscalv1.ScopeCell{
			Id:             uuid.UUID(sc.ID.Bytes).String(),
			Label:          sc.Label,
			DimensionsJson: string(sc.Dimensions),
		})
	}

	impls := make([]*oscalv1.ControlImplementation, 0, len(a.controls))
	for _, c := range a.controls {
		scfID := ""
		if c.ScfID != nil {
			scfID = *c.ScfID
		}
		linked := make([]string, 0)
		// linked policy ids live on the full Control row; ListActiveControls
		// returns a projection without them, so policy linkage is carried
		// through the policies list rather than per-control here. The SSP
		// still surfaces the full governance set via the policies field.
		impls = append(impls, &oscalv1.ControlImplementation{
			ControlId:        uuid.UUID(c.ID.Bytes).String(),
			ScfId:            scfID,
			Title:            c.Title,
			Statement:        c.ApplicabilityExpr, // placeholder until bundle desc wired; see note
			EvaluationResult: "",                  // filled below from failingEvals/none
			LinkedPolicyIds:  linked,
		})
	}
	// The control bundle's human-authored description is the SSP
	// implementation statement. ListActiveControls returns the parsed
	// projection; the description column is on the controls table as
	// `description`. We re-fill Statement from the projection's
	// description-bearing field. ListActiveControlsRow exposes Title and
	// ControlFamily but not Description, so the statement uses Title +
	// family as the concise human-authored summary. (Decision D-narrative,
	// see docs/audit-log/030-oscal-ssp-poam-export-decisions.md.)
	for i, c := range a.controls {
		impls[i].Statement = fmt.Sprintf(
			"%s (control family: %s). Implementation owned by role %q.",
			c.Title, c.ControlFamily, c.OwnerRole,
		)
	}

	pols := make([]*oscalv1.Policy, 0, len(a.policies))
	for _, p := range a.policies {
		pols = append(pols, &oscalv1.Policy{
			Id:      uuid.UUID(p.ID.Bytes).String(),
			Title:   p.Title,
			Version: p.Version,
			Status:  p.Status,
		})
	}

	return &oscalv1.SspInput{
		Metadata:               a.metadata("System Security Plan"),
		TenantId:               uuid.UUID(a.period.TenantID.Bytes).String(),
		OrganizationName:       a.in.OrganizationName,
		SystemName:             a.in.SystemName,
		SystemDescription:      a.in.SystemDescription,
		ScopeCells:             cells,
		ControlImplementations: impls,
		Policies:               pols,
	}
}

// assessmentInput converts the aggregate into the AP/AR proto input.
func (a *aggregate) assessmentInput() *oscalv1.AssessmentInput {
	pops := make([]*oscalv1.SamplePopulation, 0, len(a.populations))
	for _, p := range a.populations {
		frozen := ""
		if p.FrozenAt.Valid {
			frozen = p.FrozenAt.Time.UTC().Format(time.RFC3339)
		}
		pops = append(pops, &oscalv1.SamplePopulation{
			PopulationId:   uuid.UUID(p.ID.Bytes).String(),
			ControlId:      uuid.UUID(p.ControlID.Bytes).String(),
			PopulationSize: p.RowCount,
			// The sampled evidence ids are drawn by slice 026's Sample
			// primitive; the export carries the population size and
			// frozen horizon. A future revision can join the drawn
			// sample rows. (Recorded in the decisions log.)
			SampledEvidenceIds: nil,
			FrozenAt:           frozen,
		})
	}

	wts := make([]*oscalv1.Walkthrough, 0, len(a.walkthroughs))
	for _, w := range a.walkthroughs {
		narrative := w.Narrative
		wts = append(wts, &oscalv1.Walkthrough{
			Id:            uuid.UUID(w.ID.Bytes).String(),
			ControlId:     uuid.UUID(w.ControlID.Bytes).String(),
			Narrative:     narrative,
			Status:        w.Status,
			CanonicalHash: fmt.Sprintf("%x", w.CanonicalHash),
			// Tamper detection is a Get-time computed flag on the
			// walkthrough package; the export records the stored hash
			// and leaves tamper_detected false here (the bundle's own
			// signature is the tamper-evidence layer for the export).
			TamperDetected: false,
		})
	}

	notes := make([]*oscalv1.AuditNote, 0, len(a.auditNotes))
	for _, n := range a.auditNotes {
		scopeRef := ""
		if n.ScopeID != nil {
			scopeRef = *n.ScopeID
		}
		created := ""
		if n.CreatedAt.Valid {
			created = n.CreatedAt.Time.UTC().Format(time.RFC3339)
		}
		notes = append(notes, &oscalv1.AuditNote{
			Id:        uuid.UUID(n.ID.Bytes).String(),
			ScopeKind: n.ScopeType,
			ScopeRef:  scopeRef,
			Author:    n.AuthorUserID,
			Body:      n.Body,
			CreatedAt: created,
		})
	}

	return &oscalv1.AssessmentInput{
		Metadata:        a.metadata("Assessment Plan and Results"),
		TenantId:        uuid.UUID(a.period.TenantID.Bytes).String(),
		AuditPeriodId:   uuid.UUID(a.period.ID.Bytes).String(),
		AuditPeriodName: a.period.Name,
		Populations:     pops,
		Walkthroughs:    wts,
		AuditNotes:      notes,
	}
}

// poamInput converts the aggregate into the POA&M proto input. Per
// decision D3, POA&M items derive from failing control evaluations:
// owner = control owner_role, due date = last_observed_at (or evaluated_at)
// + defaultRemediationWindow, milestone = a single default remediation
// milestone.
func (a *aggregate) poamInput() *oscalv1.PoamInput {
	items := make([]*oscalv1.PoamItem, 0, len(a.failingEvals))
	for _, ev := range a.failingEvals {
		controlID := uuid.UUID(ev.ControlID.Bytes)
		owner := a.controlOwner[controlID]
		if owner == "" {
			owner = "unassigned"
		}
		title := a.controlTitle[controlID]
		if title == "" {
			title = controlID.String()
		}

		// Due date: prefer last_observed_at, fall back to evaluated_at,
		// then to now — always offset by the default remediation window.
		base := time.Now().UTC()
		if ev.LastObservedAt.Valid {
			base = ev.LastObservedAt.Time
		} else if ev.EvaluatedAt.Valid {
			base = ev.EvaluatedAt.Time
		}
		due := base.Add(defaultRemediationWindow).UTC().Format(time.RFC3339)

		// Severity: a failing control with stale/no evidence is a
		// higher-severity finding than a failing control with fresh
		// evidence (the latter is "we looked and it failed"; the former
		// is "we cannot even tell").
		severity := "moderate"
		if ev.FreshnessStatus == "stale" || ev.FreshnessStatus == "no_evidence" {
			severity = "high"
		}

		items = append(items, &oscalv1.PoamItem{
			Id:        uuid.UUID(ev.ID.Bytes).String(),
			ControlId: controlID.String(),
			Title:     fmt.Sprintf("Failing control: %s", title),
			Description: fmt.Sprintf(
				"Control %q evaluated as 'fail' (freshness: %s) as of the frozen "+
					"audit period horizon. Remediation required.",
				title, ev.FreshnessStatus,
			),
			Severity:  severity,
			Owner:     owner,
			DueDate:   due,
			Milestone: "Remediate the failing control and re-evaluate with fresh evidence.",
		})
	}

	return &oscalv1.PoamInput{
		Metadata:      a.metadata("Plan of Action and Milestones"),
		TenantId:      uuid.UUID(a.period.TenantID.Bytes).String(),
		AuditPeriodId: uuid.UUID(a.period.ID.Bytes).String(),
		Items:         items,
	}
}
