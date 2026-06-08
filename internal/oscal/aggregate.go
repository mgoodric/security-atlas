package oscal

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	controls     []dbx.ListActiveControlsWithDescriptionRow
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
	// acceptedVendorClaims are operator-ACCEPTED vendor claims (slice 512
	// import + slice 589 disposition) surfaced in the SSP as VENDOR-ATTESTED
	// by-component statements (slice 619). HARD BOUNDARY (P0-619 / inherits
	// P0-512-1 / invariant #2): these are vendor ASSERTIONS the operator chose
	// to credit, NOT platform-verified evidence. They are carried in a SEPARATE
	// field from `controls`, never contribute to control-satisfaction/coverage,
	// and nothing here writes control_evaluations (the read is pure SELECT over
	// imported_component_claims).
	acceptedVendorClaims []dbx.ListAcceptedVendorClaimsForExportRow
	// sampledEvidence maps population_id -> the DRAWN sample evidence ids,
	// in shuffle order (slice 494, AC-1/AC-2). Read from the persisted
	// sample_evidence rows, which were materialized at draw-time over the
	// frozen population — so the draw is frozen-correct by construction
	// (invariant #10) and never re-sampled from live data (D1).
	sampledEvidence map[uuid.UUID][]uuid.UUID
	// walkthroughAttachments maps walkthrough_id -> its attachment refs
	// (slice 494, AC-4/AC-5). Metadata only — the attachment bytes are
	// never read; the AR references them by hash + storage URI (D2).
	walkthroughAttachments map[uuid.UUID][]dbx.ListWalkthroughAttachmentsForPeriodRow
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
		in:                     in,
		controlOwner:           map[uuid.UUID]string{},
		controlTitle:           map[uuid.UUID]string{},
		sampledEvidence:        map[uuid.UUID][]uuid.UUID{},
		walkthroughAttachments: map[uuid.UUID][]dbx.ListWalkthroughAttachmentsForPeriodRow{},
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

	// 3. Active controls (SSP control-implementations). Slice 493: read
	//    the description-bearing projection so the SSP implementation
	//    Statement carries the control bundle's authored narrative
	//    (canvas §8.2), not a synthesized placeholder. The read runs under
	//    the same tenant RLS context + the same transaction as the rest of
	//    the aggregate, so it is tenant-scoped (invariant #6) and
	//    point-in-time consistent with the other reads (AC-5, threat-model
	//    T + I).
	controls, err := q.ListActiveControlsWithDescription(ctx, pgUUID(tenantID))
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

	// 4b. Operator-ACCEPTED vendor claims (slice 619). Surfaced in the SSP as
	//     VENDOR-ATTESTED by-component statements — clearly attributed to the
	//     vendor component, never as platform-verified evidence (P0-619 /
	//     inherits P0-512-1 / invariant #2). This is a pure READ over
	//     imported_component_claims (claim_status = 'accepted'); it writes
	//     NOTHING (no control_evaluations, no evidence ledger) and contributes
	//     ZERO to control-satisfaction/coverage. Tenant-scoped via RLS + the
	//     leading tenant predicate (invariant #6). Claims are tenant-wide, not
	//     period-scoped: an accepted vendor attestation is a standing fact
	//     about the operator's program, not a frozen-period sample.
	acceptedClaims, err := q.ListAcceptedVendorClaimsForExport(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("oscal: list accepted vendor claims: %w", err)
	}
	agg.acceptedVendorClaims = acceptedClaims

	// 5. Sample populations attached to this period (AP).
	populations, err := q.ListPopulationsForPeriod(ctx, dbx.ListPopulationsForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list populations for period: %w", err)
	}
	agg.populations = populations

	// 5b. Drawn sample evidence ids per population (slice 494, AC-1/AC-2).
	//     Reads the persisted sample_evidence rows — the realized draw
	//     materialized at draw-time over the FROZEN population (D1). The
	//     query never touches live evidence_records, so a post-freeze
	//     record cannot appear here (invariant #10, AC-7). Same tx + tenant
	//     RLS context as every other read (invariant #6, AC-8).
	sampledRows, err := q.ListSampledEvidenceForPeriod(ctx, dbx.ListSampledEvidenceForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list sampled evidence for period: %w", err)
	}
	// Rows arrive ordered by (population_id, ordinal); appending preserves
	// the deterministic shuffle order the auditor's sample carries (AC-9).
	for _, r := range sampledRows {
		popID := uuid.UUID(r.PopulationID.Bytes)
		agg.sampledEvidence[popID] = append(
			agg.sampledEvidence[popID], uuid.UUID(r.EvidenceRecordID.Bytes),
		)
	}

	// 6. Walkthroughs pinned to this period (AR observations).
	walkthroughs, err := q.ListWalkthroughsForPeriod(ctx, dbx.ListWalkthroughsForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list walkthroughs for period: %w", err)
	}
	agg.walkthroughs = walkthroughs

	// 6b. Walkthrough attachment references (slice 494, AC-4/AC-5).
	//     Metadata only (id, storage_key, content_type, sha256, annotations)
	//     — the attachment BYTES are never read (P0-494-2). Tenant-scoped via
	//     the join + RLS; rows arrive grouped by walkthrough then upload
	//     order so the per-walkthrough cap (D3) selects a stable prefix.
	attRows, err := q.ListWalkthroughAttachmentsForPeriod(ctx, dbx.ListWalkthroughAttachmentsForPeriodParams{
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(in.AuditPeriodID),
	})
	if err != nil {
		return nil, fmt.Errorf("oscal: list walkthrough attachments for period: %w", err)
	}
	for _, r := range attRows {
		wtID := uuid.UUID(r.WalkthroughID.Bytes)
		agg.walkthroughAttachments[wtID] = append(agg.walkthroughAttachments[wtID], r)
	}

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

// fallbackStatementLabel prefixes a synthesized control-implementation
// statement so an auditor reading the SSP cannot mistake it for the
// operator's authored narrative. Slice 493 JUDGMENT decision D-fallback:
// the label is deliberately explicit and front-loaded — an auditor skims
// the first words of each statement, so the honesty marker must lead.
const fallbackStatementLabel = "[Auto-generated summary — no authored implementation narrative on file.]"

// controlStatement returns the SSP control-implementation statement for a
// control row. Slice 493 (resolves slice 030 D-narrative):
//
//   - When the control bundle carries a human-authored `description`
//     (slice 009), that description IS the statement, verbatim (AC-2).
//     This is the narrative §8.2 calls for — how the control is actually
//     implemented — and it is never AI-generated (CLAUDE.md
//     product-runtime AI-assist boundary).
//   - When the control has no authored description (a manual/minimal
//     bundle), fall back to a CLEARLY-LABELED synthesized summary (AC-3)
//     so the statement is never empty (P0-493-1) and an auditor is not
//     misled into thinking the boilerplate is authored.
//
// The statement is filled exactly once at the call site (AC-4); the old
// ApplicabilityExpr placeholder + double-fill are gone.
func controlStatement(c dbx.ListActiveControlsWithDescriptionRow) string {
	if desc := strings.TrimSpace(c.Description); desc != "" {
		return desc
	}
	return fmt.Sprintf(
		"%s Control %q (family: %s); implementation owned by role %q. "+
			"Provide an authored control-implementation narrative to replace this placeholder.",
		fallbackStatementLabel, c.Title, c.ControlFamily, c.OwnerRole,
	)
}

// sspInput converts the aggregate into the SSP proto input. The control
// implementation `statement` is the human-authored control description —
// never AI-generated (CLAUDE.md product-runtime AI-assist boundary). When a
// control has no authored description the statement falls back to a clearly
// labeled synthesized summary (controlStatement / slice 493).
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
		// linked policy ids live on the full Control row; the active-controls
		// projection omits them, so policy linkage is carried through the
		// policies list rather than per-control here. The SSP still surfaces
		// the full governance set via the policies field.
		impls = append(impls, &oscalv1.ControlImplementation{
			ControlId:        uuid.UUID(c.ID.Bytes).String(),
			ScfId:            scfID,
			Title:            c.Title,
			Statement:        controlStatement(c), // filled exactly once (AC-4)
			EvaluationResult: "",                  // filled below from failingEvals/none
			LinkedPolicyIds:  linked,
		})
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
		// Operator-accepted vendor claims, carried in a SEPARATE field
		// (slice 619). They are rendered as vendor-attested by-component
		// statements and NEVER merged into ControlImplementations — a vendor
		// claim never contributes to platform control-satisfaction (the hard
		// boundary). The list is empty when no claim has been accepted.
		VendorAttestedImplementations: a.vendorAttestedImplementations(),
	}
}

// vendorAttestedImplementations converts the operator-accepted vendor claims
// into proto messages for the SSP (slice 619).
//
// HARD CONSTITUTIONAL BOUNDARY (P0-619 / inherits P0-512-1 / invariant #2):
// every value here originates from imported_component_claims — a vendor
// ASSERTION the operator chose to credit, NOT platform-verified evidence. The
// proto carries no evaluation_result and the bridge renders these as
// `by-component` statements attributed to the vendor component, flagged
// vendor-attested + operator-credited, with the accept-provenance. They are
// NEVER folded into ControlImplementations and contribute ZERO to coverage.
func (a *aggregate) vendorAttestedImplementations() []*oscalv1.VendorAttestedImplementation {
	out := make([]*oscalv1.VendorAttestedImplementation, 0, len(a.acceptedVendorClaims))
	for _, c := range a.acceptedVendorClaims {
		scfID := ""
		if c.ScfAnchorID != nil {
			scfID = *c.ScfAnchorID
		}
		acceptedBy := ""
		if c.DispositionedBy != nil {
			acceptedBy = *c.DispositionedBy
		}
		acceptedAt := ""
		if c.DispositionedAt.Valid {
			acceptedAt = c.DispositionedAt.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, &oscalv1.VendorAttestedImplementation{
			ClaimId:         uuid.UUID(c.ClaimID.Bytes).String(),
			ControlId:       c.ControlID,
			ScfId:           scfID,
			ComponentUuid:   c.ComponentUuid,
			ComponentTitle:  c.ComponentTitle,
			ComponentType:   c.ComponentType,
			Statement:       c.Statement,
			AcceptedBy:      acceptedBy,
			AcceptedAt:      acceptedAt,
			DispositionNote: c.DispositionNote,
		})
	}
	return out
}

// assessmentInput converts the aggregate into the AP/AR proto input.
func (a *aggregate) assessmentInput() *oscalv1.AssessmentInput {
	pops := make([]*oscalv1.SamplePopulation, 0, len(a.populations))
	for _, p := range a.populations {
		frozen := ""
		if p.FrozenAt.Valid {
			frozen = p.FrozenAt.Time.UTC().Format(time.RFC3339)
		}
		popID := uuid.UUID(p.ID.Bytes)
		// AC-1/AC-2/AC-3: carry the DRAWN sample evidence ids, read from the
		// persisted sample_evidence rows (the realized draw materialized at
		// draw-time over the frozen population — D1). sampledFor returns the
		// ids as stable strings in shuffle order; a population never sampled
		// carries an empty slice (honest "nothing drawn yet"), not nil noise.
		pops = append(pops, &oscalv1.SamplePopulation{
			PopulationId:       popID.String(),
			ControlId:          uuid.UUID(p.ControlID.Bytes).String(),
			PopulationSize:     p.RowCount,
			SampledEvidenceIds: a.sampledFor(popID),
			FrozenAt:           frozen,
		})
	}

	wts := make([]*oscalv1.Walkthrough, 0, len(a.walkthroughs))
	for _, w := range a.walkthroughs {
		narrative := w.Narrative
		wtID := uuid.UUID(w.ID.Bytes)
		wts = append(wts, &oscalv1.Walkthrough{
			Id:            wtID.String(),
			ControlId:     uuid.UUID(w.ControlID.Bytes).String(),
			Narrative:     narrative,
			Status:        w.Status,
			CanonicalHash: fmt.Sprintf("%x", w.CanonicalHash),
			// Tamper detection is a Get-time computed flag on the
			// walkthrough package; the export records the stored hash
			// and leaves tamper_detected false here (the bundle's own
			// signature is the tamper-evidence layer for the export).
			TamperDetected: false,
			// AC-4/AC-5: attachment references (hash + storage URI only,
			// bytes never embedded — P0-494-2), capped per-walkthrough (D3).
			Attachments: a.walkthroughAttachmentRefs(wtID),
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

// maxAttachmentRefsPerWalkthrough caps the attachment references the AR
// carries for a single walkthrough (slice 494 D3, threat-model D). Beyond
// the cap the export carries the first N (stable upload order) plus one
// synthetic overflow ref that names the total — evidence is never silently
// dropped. Revisit once real walkthroughs exceed it; the overflow note makes
// the cap safe to tune without a schema change.
const maxAttachmentRefsPerWalkthrough = 50

// sampledFor returns the drawn sample evidence ids for a population as
// stable strings in the deterministic shuffle order (AC-1/AC-9). A
// population that was never sampled returns an empty (non-nil) slice so the
// AR honestly reports "population of M, nothing drawn yet" rather than
// omitting the field.
func (a *aggregate) sampledFor(populationID uuid.UUID) []string {
	ids := a.sampledEvidence[populationID]
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// walkthroughAttachmentRefs maps a walkthrough's stored attachment metadata
// to OSCAL-bound attachment references (AC-4). The attachment BYTES are never
// touched (P0-494-2): each ref carries the content hash + the object-storage
// URI (the slice-036 storage_key) only. The list is capped at
// maxAttachmentRefsPerWalkthrough (D3); on overflow a final synthetic ref
// names the total so the auditor knows the full set exists.
func (a *aggregate) walkthroughAttachmentRefs(walkthroughID uuid.UUID) []*oscalv1.WalkthroughAttachment {
	rows := a.walkthroughAttachments[walkthroughID]
	if len(rows) == 0 {
		return nil
	}

	capped := rows
	overflow := 0
	if len(rows) > maxAttachmentRefsPerWalkthrough {
		overflow = len(rows) - maxAttachmentRefsPerWalkthrough
		capped = rows[:maxAttachmentRefsPerWalkthrough]
	}

	refs := make([]*oscalv1.WalkthroughAttachment, 0, len(capped)+1)
	for _, r := range capped {
		refs = append(refs, &oscalv1.WalkthroughAttachment{
			Id:            uuid.UUID(r.ID.Bytes).String(),
			Filename:      attachmentFilename(r.StorageKey),
			ContentHash:   r.Sha256Hash,
			ContentType:   r.ContentType,
			AnnotationRef: annotationRef(r.Annotations),
			StorageUri:    r.StorageKey,
		})
	}
	if overflow > 0 {
		// Synthetic overflow ref: no id/hash/uri — its filename is the
		// honest "N more attachments" note (D3). Never silently truncate.
		refs = append(refs, &oscalv1.WalkthroughAttachment{
			Filename: fmt.Sprintf(
				"%d additional attachment(s) not shown; see the walkthrough record for the full set.",
				overflow,
			),
		})
	}
	return refs
}

// attachmentFilename derives a human-readable filename from the
// object-storage key. The slice-036 key format is tenant-{uuid}/{uuid}; the
// trailing segment is the stable identifier an auditor sees. If the key has
// no separator the whole key is returned.
func attachmentFilename(storageKey string) string {
	if i := strings.LastIndexByte(storageKey, '/'); i >= 0 && i < len(storageKey)-1 {
		return storageKey[i+1:]
	}
	return storageKey
}

// annotationRef renders the attachment's annotation jsonb as a compact
// reference string for the AR. The annotations column is a free-form
// {regions: [...]} blob (slice 027); the AR carries it verbatim as the
// annotation reference (AC-4) so an auditor importing the AR can correlate
// the region metadata. An empty / "{}" blob yields the empty string.
func annotationRef(annotations []byte) string {
	s := strings.TrimSpace(string(annotations))
	if s == "" || s == "{}" {
		return ""
	}
	return s
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
