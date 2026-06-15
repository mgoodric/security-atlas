package evidencesummary

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// PeriodEvidenceSet is the FROZEN-population deterministic evidence corpus for
// one control WITHIN one frozen audit period (slice 749). It embeds the same
// EvidenceSet the live surface uses (so the shared Service pipeline consumes it
// unchanged) and adds the freeze-horizon metadata the audit-workspace UI labels
// the summary with (period-scoped + frozen-as-of FrozenAt — AC-4) and that the
// horizon-bounded citation resolver needs.
type PeriodEvidenceSet struct {
	EvidenceSet

	// AuditPeriodID is the frozen audit period the corpus is bounded by.
	AuditPeriodID uuid.UUID
	// FrozenAt is the period's freeze horizon. The corpus draws ONLY from
	// evidence with observed_at <= FrozenAt (invariant #10, P0-749-1). Surfaced
	// to the UI as the "frozen as of" label.
	FrozenAt time.Time
}

// PeriodSummary is the result of PeriodService.PeriodSummarize. It embeds the
// same Summary the live surface returns (Text/Citations/Suppressed/Reason/Model*
// — identical NON-BINDING, read-only, never-persisted contract) and carries the
// freeze-horizon metadata for the period-scoped + frozen-as-of UI label (AC-4).
type PeriodSummary struct {
	Summary

	AuditPeriodID uuid.UUID
	FrozenAt      time.Time
}

// PeriodEvidenceReader assembles the deterministic bounded FROZEN-population
// evidence set for one control within one frozen audit period. The production
// implementation is *PeriodStore; tests supply a fake so the suppression +
// frozen-population branches are exercised without a live Postgres on the unit
// surface (the integration tier uses the real *PeriodStore).
type PeriodEvidenceReader interface {
	PeriodEvidenceSet(ctx context.Context, controlID, auditPeriodID uuid.UUID) (PeriodEvidenceSet, error)
}

// PeriodCitationResolver resolves a candidate cited ID to a tenant-owned row
// bounded by the period freeze horizon (a post-freeze evidence id is NOT
// citable — P0-749-1, AC-5). The production implementation is *PeriodStore
// (ResolveBeforeHorizon); tests supply a fake.
type PeriodCitationResolver interface {
	ResolveBeforeHorizon(ctx context.Context, id, controlID uuid.UUID, frozenAt time.Time) (Citation, bool, error)
}

// PeriodService is the slice-749 audit-workspace orchestrator. It is a THIN
// variant of *Service: it assembles the FROZEN-population evidence set, then runs
// the IDENTICAL slice-502 pipeline (one bounded generation against the per-tenant
// inference client, validate-every-citation-then-suppress, graceful degradation).
// It holds no per-call state and persists nothing (P0-502-4 carried forward).
//
// The reuse is literal: PeriodSummarize delegates the generate + validate +
// suppress machinery to runSummary (the shared pipeline factored out of
// Service.Summarize), passing the frozen EvidenceSet as the corpus and a
// horizon-bound citation resolver. The ONLY differences from the live surface
// are the corpus (frozen, not live) and the citable-id horizon (frozen-population
// only).
type PeriodService struct {
	reader   PeriodEvidenceReader
	client   llm.Client
	resolver PeriodCitationResolver
}

// NewPeriodService wires the frozen-population reader, the inference client (the
// slice-499 per-tenant router in production, the Stub in CI), and the
// horizon-bound citation resolver. In production the reader + resolver are both
// backed by the same *PeriodStore.
func NewPeriodService(reader PeriodEvidenceReader, client llm.Client, resolver PeriodCitationResolver) *PeriodService {
	return &PeriodService{reader: reader, client: client, resolver: resolver}
}

// PeriodSummarize produces the period-scoped evidence summary for one control
// within one frozen audit period, in the caller's tenant context. It ALWAYS
// returns the deterministic bounded FROZEN-population EvidenceSet (AC-7,
// P0-502-7 carried forward); the plain-language Text + Citations are present only
// when generation succeeded AND every citation resolved to a tenant-owned row
// WITHIN the frozen population (AC-2, P0-749-1). On any model/citation failure
// the returned PeriodSummary has Suppressed=true and a fixed Reason, and the
// caller renders the frozen evidence list alone.
//
// The only errors PeriodSummarize returns are genuine read failures: ErrNoPeriod
// (period absent/cross-tenant), ErrPeriodNotFrozen (open period — no frozen
// population), ErrNoControl (control absent/cross-tenant), or a DB error. A
// model/citation failure is NOT an error: it is graceful degradation conveyed via
// Suppressed.
func (s *PeriodService) PeriodSummarize(ctx context.Context, controlID, auditPeriodID uuid.UUID) (PeriodSummary, error) {
	set, err := s.reader.PeriodEvidenceSet(ctx, controlID, auditPeriodID)
	if err != nil {
		return PeriodSummary{}, err
	}

	// Bind the freeze horizon into the citation resolver so the Service's
	// validate-citation gate enforces frozen-population membership (P0-749-1) in
	// addition to the grounding gate (which the frozen EvidenceSet already
	// satisfies, since its Records exclude post-freeze evidence).
	bound := &boundPeriodResolver{
		resolver:  s.resolver,
		controlID: controlID,
		frozenAt:  set.FrozenAt,
	}

	sum := runSummary(ctx, s.client, bound, set.EvidenceSet)
	return PeriodSummary{
		Summary:       sum,
		AuditPeriodID: set.AuditPeriodID,
		FrozenAt:      set.FrozenAt,
	}, nil
}

// boundPeriodResolver adapts a PeriodCitationResolver (which needs the control +
// horizon to bound the read) to the horizon-free CitationResolver interface the
// shared pipeline calls. It captures the per-request (controlID, frozenAt) so the
// pipeline stays identical to the live surface.
type boundPeriodResolver struct {
	resolver  PeriodCitationResolver
	controlID uuid.UUID
	frozenAt  time.Time
}

func (b *boundPeriodResolver) Resolve(ctx context.Context, id uuid.UUID) (Citation, bool, error) {
	return b.resolver.ResolveBeforeHorizon(ctx, id, b.controlID, b.frozenAt)
}
