# Slice 751 — Board-narrative exception-status section + the deterministic exceptions aggregate (decisions log)

JUDGMENT slice. Two deliverables, in dependency order: the **deterministic,
RLS-scoped exceptions aggregate** in `board.Brief`, and then the **AI-drafted
exception-status section** that grounds on it. The aggregate is the real work —
slice 501 deliberately did NOT ship this section because exceptions were not in
`board.Brief`, and an AI-drafted exception claim with no ground truth behind it
is unverifiable at guardrail 5. That is the one failure mode the four gates exist
to prevent, so the prerequisite was never optional.

This slice adds NO new guardrails and weakens none — the section is a new
`SectionDef` consuming the slice-501 machinery unchanged (P0-501-6). The runtime
**AI-assist boundary is constitutional and untouched**: the product still never
publishes a board-binding artifact without one-click human approval, never
fabricates a number, and never seeds Tenant B with Tenant A data. This log is a
development-process artifact, not a relaxation of that boundary.

- detection_tier_actual: none
- detection_tier_target: none

> No defect surfaced during the slice. The section reuses the proven pipeline; the
> only net-new production code is the aggregate read (the
> `BoardBriefExceptionAggregate` query, `Store.ExceptionAggregate`, and the pure
> `exceptionSummary` projection) plus a declarative `SectionDef`. The aggregate's
> edge branches (empty register, NULL `MIN`, future `effective_from`) were written
> against tests first and none regressed.

---

## D1 — Which exception numbers are board-grade (THE judgment call — AC-5)

A board is being asked to understand **accepted risk**, not to administer a
workflow. That principle decides the whole set. Three numbers ship:

| Number                   | Column source                                              | Why a board hears it                                                                                                                  |
| ------------------------ | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `active_count`           | `COUNT(*) FILTER (WHERE status='active')`                  | The accepted-risk headline: how many controls the program has explicitly, formally decided not to meet right now.                     |
| `past_due_count`         | above, `AND expires_at <= as_of`                           | Governance hygiene: a waiver still in force after the sunset date it was granted under. Non-zero is exactly what a board should hear. |
| `oldest_active_age_days` | `MIN(COALESCE(effective_from, approved_at, requested_at))` | The permanent-exception smell: a waiver standing for hundreds of days is a decision that quietly became architecture.                 |

**Deliberately NOT board-grade**, and therefore **absent from the aggregate** so
the section cannot state them at all:

- **Requested / pending counts.** Workflow backlog — the operator's queue, not
  the board's. A pending waiver is not accepted risk; it is an open ticket.
- **Denied and expired counts.** Historical churn. A denied waiver is the process
  working; reporting it invites a board to read process noise as posture.
- **Any per-control or per-scope breakdown.** A board pack is not an exception
  register export. Per-case detail is where a non-technical reader over-indexes
  on one vivid item and misses the aggregate posture.

The exclusion is enforced structurally, not by prompt discipline: the excluded
facts are never read into `ExceptionSummary`, so `Rollup.AllowedNumbers()` cannot
return them and guardrail 5 rejects any draft that states one. **A number that is
not board-grade is not merely discouraged — it is ungroundable.**

### Why the aggregate did not become its own OE

The issue offered "if this is substantial, file it as its own OE and stop." It is
not substantial: one sqlc query, one `Store` method, one pure projection function,
and one struct field. The `exceptions` table (slice 021) already carries every
column the three numbers need, already has `FORCE ROW LEVEL SECURITY`, and already
has the `(tenant_id, status, expires_at)` index the aggregate's filters ride. There
was no new source plumbing to design — only a projection the board owns. Splitting
it would have cost a full round-trip to deliver a ~90-line read.

---

## D2 — Aggregate shape: board-owned projection, timestamp not day-count

Two shape calls inside the aggregate:

**The query lives in `board_briefs.sql`, not `exceptions.sql`.** Same reason
`ListRisksForBoardBrief` does: the board brief's read of another module's table is
a projection the **board** owns. A change to the exception module's own query set
must never silently change what the board reports.

**`oldest_active_started_at` is returned as a TIMESTAMPTZ, not a day count.** The
age arithmetic happens in Go, in `exceptionSummary`, against the brief's
`generatedAt` — so exception aging uses the same clock (and the same overridable
test clock, `WithClock`) as the risk-aging path, rather than the DB's `now()`. Two
time sources in one brief is how a board pack ends up internally inconsistent by a
day at a period boundary.

The `COALESCE(effective_from, approved_at, requested_at)` start chain mirrors the
slice-021 lifecycle: `effective_from` is set at activation, `approved_at` at
approval, `requested_at` always. The fallback chain means an active waiver always
has a start instant, so the age is never undefined.

Two edge branches are deliberate and tested:

- **Empty register / NULL `MIN`** → age `0`, not the age since the zero time. An
  empty exception register is an honest zero posture, **not** an error — zero
  active exceptions is board-relevant and the section reports it as zero.
- **Future `effective_from`** (operator scheduled a waiver ahead of the brief date)
  → clamps to `0`, never a negative age. A negative day count in a board narrative
  reads as a bug and destroys trust in every other number on the page.

---

## D3 — RLS scoping: the load-bearing property (AC-1)

The aggregate runs inside the same tenant-scoped transaction as every other
`Store` read, so the slice-021 `FORCE ROW LEVEL SECURITY` policy on `exceptions`
applies. The explicit `tenant_id` predicate is the primary filter; RLS is
defense-in-depth (invariant #6).

The distinction that matters: another tenant's waiver is **invisible to the
count**, not filtered out of it. A board-facing count that could silently include
a foreign tenant's waiver would be worse than no count at all — it would be a
confidently-stated wrong number, which is precisely the asymmetric-hallucination
failure the four gates are built around.

`TestIntegration_ExceptionAggregate_RLSScoped` proves it against a real Postgres
with the `atlas_app` (NOBYPASSRLS) role: tenant B's aggregate does not see tenant
A's exceptions.

---

## D4 — Frozen pre-751 briefs deserialize the aggregate as zero (and why that is safe)

`board_briefs` is append-only; a brief frozen before this slice has no
`exceptions` key in its stored JSONB and deserializes `ExceptionSummary` as the
zero value. In the **stored content** that is indistinguishable from a genuinely
empty exception register.

This is safe because **the AI-narrative surface never reads a stored brief.** It
grounds on a live `boardGen.Assemble` (a pure read, no `board_briefs` write —
`internal/api/register_board.go`), which always populates the aggregate. So a
frozen historical brief can never supply a stale or absent exception number to a
draft. Old reports stay immutable, which is the intended snapshot-at-generation
semantics — not a migration gap to backfill.

---

## D5 — The section runs the four gates unchanged (AC-2/3/4/6)

`SectionExceptionStatus` is a plain `SectionDef` registered in `sectionDefs` and
appended to `AIDraftedSections`. No pipeline change. Each gate applies:

| Gate                           | How it applies to this section                                                                                               |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------- |
| 1 — mandatory citations        | Item 3 must cite a control/evidence id from the rollup's bounded excerpt set; an unresolved id suppresses the draft.         |
| 5 — numeric-claim verification | `AllowedNumbers()` returns **only** the three aggregate integers under the `exceptionOnly` discriminator.                    |
| 6 — section-shape enforcement  | Heading verbatim + exactly 3 numbered items (`exceptionExpectedItems = 3`); freestyle output rejected.                       |
| 2 — per-section approval       | The section is independently approvable; suppression is per-section — the other three still draft when this one is rejected. |

The `exceptionOnly` discriminator is load-bearing: it means a coverage percentage
or a risk severity incidentally carried in the same `Rollup` struct can **never**
validate a number in an exception draft. Each section's ground truth is disjoint.

**Banned phrases** are enforced identically to every other section —
`buildExceptionSystemPrompt` interpolates the shared `BannedPhraseListForPrompt()`
into the placeholder, so the section inherits the generalized slice-501 list with
no per-section carve-out (`TestExceptionSystemPrompt_EmbedsBannedPhraseList`).

The prompt additionally forbids characterizing the count as acceptable,
concerning, or improving. An exception count is a fact; the _judgment_ about
whether it is a good number is the board's to make, and a model that editorializes
on accepted risk is doing governance it has no standing to do.

### Auto-rejection proof (AC-3, the criterion the slice exists for)

- `TestExceptionSection_FabricatedNumberAutoRejected` — aggregate says 4 in force,
  draft claims 12: suppressed with `ReasonNumericMismatch`, no draft text or
  record id returned, nothing persisted, **and the forensic audit row is still
  written** (guardrail 3 — a suppressed draft is exactly what an auditor wants
  reconstructable).
- `TestExceptionSection_EachAggregateNumberIsPinned` — all three numbers pinned
  independently; fabricating any one rejects the whole draft.
- `TestIntegration_ExceptionSection_FabricatedNumberAutoRejected` — the same
  end-to-end against Postgres, additionally proving the other three sections still
  draft normally (suppression is per-section, as per-section approval requires).

---

## Revisit once in use (named follow-ons — NOT built here)

1. **Exception trend over the period.** "Active count moved from N to M since last
   quarter" would be the natural fourth number, but it needs a prior-period
   aggregate the brief does not carry. Same discipline that deferred this whole
   section from slice 501: no ground truth, no claim.
2. **Exception-status surfacing in the board pack PDF.** The section flows into the
   pack through the existing approved-section assembly; no PDF-specific work was
   needed. Worth a look once an operator has run a real pack with exceptions in it.
