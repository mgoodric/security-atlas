# 749 — Period-scoped evidence summary (respects audit-period freezing)

**Cluster:** AI-assist
**Estimate:** M (1-2d)
**Type:** JUDGMENT (the frozen-population labeling + the audit-workspace mount)
**Status:** `ready` (depends on #502 merged + the slice-028/008 audit-period
freeze primitives, all on `main`)

> Surfaced during slice 502, captured as follow-up per continuous-batch policy.
> Slice 502 shipped evidence-summarization v0 over **current live** evidence
> only (P0-502-5, invariant #10), and named a period-scoped summary as an
> explicit follow-on (502 decisions-log "Revisit once in use" #1). This is that
> follow-on.

## Narrative

**Why.** Slice 502 summarizes a control's _current live_ evidence — the right
corpus for the "what does my evidence show right now" question. But during an
**in-progress audit**, the operator (and the auditor) reason over the **frozen
audit-period sample population** (`observed_at ≤ frozen_at`), not live state.
There is no AI comprehension aid over that frozen population today; the operator
reads the frozen sample list record by record, exactly the gap 502 closed for
live evidence.

**What.** A period-scoped variant of the 502 surface: for one control **within a
frozen AuditPeriod**, retrieve the bounded top-N evidence records drawn ONLY from
the frozen sample population (`observed_at ≤ frozen_at` — invariant #10), feed
cited excerpts to the slice-499 per-tenant inference client, and render a
non-binding, cited summary in the **audit workspace** (not the live
control-detail view), clearly labeled as period-scoped + frozen-as-of `frozen_at`.

**Scope discipline.** Mirror 502's entire constitutional contract
(validate-every-citation-then-suppress, no fabricated coverage, cross-tenant
proven absent, no approve/publish/export, never persisted, local-default
routing). The ONLY difference from 502 is the corpus (frozen population, not
live) and the mount (audit workspace, not control-detail). It does NOT relax
audit-period freezing — the summary is a comprehension aid OVER the frozen
sample, never a new sample-population source, never an audit-binding artifact.

## Threat model

Same STRIDE family as 502, plus one sharpened concern:

**T — frozen-population integrity.** The summary must draw ONLY from
`observed_at ≤ frozen_at`. A bug that let a post-freeze (live) record into the
summarized set would be audit-period pollution (the constitution's named
anti-pattern), even non-binding.
_Mitigation/AC:_ the retrieval query is the slice-028 frozen-sample read path
(NOT 502's live `ListEvidenceRecordsByControl`); an integration test proves a
record with `observed_at > frozen_at` never appears in the period-scoped summary
or its citable-id set.

All other STRIDE rows inherit 502's mitigations verbatim (control-detail/audit
authz, RLS tenant isolation, bounded corpus, local-default inference).

## Acceptance criteria

- [ ] **AC-1.** For one control in a frozen AuditPeriod, the bounded top-N
      evidence set is drawn ONLY from `observed_at ≤ frozen_at` (invariant #10).
- [ ] **AC-2.** Same validate-every-citation-then-suppress gate as 502 (no
      fabricated coverage; P0-502-1 carried forward).
- [ ] **AC-3.** Cross-tenant isolation proven (a Tenant-B period summary cannot
      cite a Tenant-A record).
- [ ] **AC-4.** Mounted in the audit workspace, clearly labeled period-scoped +
      `frozen_at`; non-binding, no approve/publish/export, never persisted.
- [ ] **AC-5.** Integration test: a post-freeze live record NEVER enters the
      period-scoped summary (frozen-population integrity).
- [ ] **AC-6.** Decisions log + changelog.

## Anti-criteria (P0 — block merge)

- **P0-749-1.** Does NOT mix live and frozen populations — frozen sample only.
- **P0-749-2.** Does NOT relax audit-period freezing (invariant #10).
- **P0-749-3.** Inherits ALL 502 anti-criteria (no fabricated coverage, no
  cross-tenant bleed, non-binding/read-only, never persisted, local default,
  graceful degradation, bounded corpus).

## Dependencies

- **#502 (merged)** — the live evidence-summary surface this mirrors.
- Slice-028 / slice-008 audit-period freeze + frozen-sample read path (on `main`).
- Reuses the slice-499 per-tenant inference client.

## Notes

This is a thin variant of 502 — most of the cost is the frozen-sample retrieval
query + the audit-workspace mount + the frozen-population integrity test. Reuse
`internal/evidencesummary`'s Service/citation/prompt machinery (the corpus
assembly is the only new piece — inject a different `EvidenceReader`).
