# Slice 749 — period-scoped evidence summary (respects audit-period freezing) — decisions log

`Type: JUDGMENT`. Claude made the subjective build-time calls itself and
recorded them here; the maintainer iterates post-deployment from the "Revisit
once in use" list. This log does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. All integration ACs — frozen-population
integrity / post-freeze-record-never-enters (AC-5), valid frozen render,
fabricated-id suppression (AC-2), cross-tenant suppression (AC-3),
unknown-period not-found, open-period-refused — passed first run against real
Postgres + RLS via the slice-498 `StubClient` CI seam. The pure-Go service +
handler suppression / horizon-binding branches passed first run on the fast
surface. The web view-model passed first run on vitest.)

## Context

Slice 749 is the audit-workspace **sibling** of slice 502 (the live
control-detail evidence summary). Where 502 summarizes a control's CURRENT LIVE
evidence ("what does my evidence show right now"), 749 summarizes the FROZEN
audit-period sample population ("what does the frozen evidence in this audit
period show"). The slice was deliberately built as a thin variant of 502: it
reuses 502's entire constitutional contract verbatim and changes only the corpus
(frozen, not live) and the mount (audit workspace, not control-detail). The
decisions below record the JUDGMENT calls the slice doc flags (the
frozen-population labeling, the audit-workspace mount, the extend-vs-sibling
package choice) and re-affirm the inherited 502 calls.

## Decisions made

### D1 — Extend `internal/evidencesummary` rather than spin a new package. `high`

**Options considered.** (a) A whole new `internal/periodevidencesummary` package
duplicating the Service/citation/prompt machinery. (b) EXTEND
`internal/evidencesummary` with a sibling `PeriodService` + `PeriodStore` that
reuse the existing pipeline.

**Chosen: (b).** The slice doc's Notes section is explicit ("Reuse
`internal/evidencesummary`'s Service/citation/prompt machinery … inject a
different `EvidenceReader`"). To make the reuse literal I factored the
generate + validate-citation + suppress body out of `Service.Summarize` into a
package-private `runSummary(ctx, client, resolver, set)` that BOTH surfaces call.
`PeriodService.PeriodSummarize` assembles the frozen `EvidenceSet`, then hands it
to the identical `runSummary` — so the no-fabricated-coverage /
graceful-degradation / bounded-top-N contract is the SAME code path, not a
re-implementation. The cost is one small refactor of 502's `Summarize` (which its
own integration suite re-verified green). Keeping it in the same package also
keeps it in integration shard B4 and reuses the existing coverage floors. A new
package would have duplicated the pipeline and invited the two surfaces to
drift — exactly the failure the §10.2 "thin variant" framing is meant to avoid.

### D2 — Frozen-population labeling: `frozen` + `frozen_at` on the wire, "Period-scoped · frozen as of <date>" badge in the UI. `high`

**Options considered.** (a) Reuse 502's `live_only:true` marker (inverted).
(b) A distinct `frozen:true` + `frozen_at` (RFC3339) wire pair, surfaced as an
always-rendered badge.

**Chosen: (b).** The audit-period-pollution anti-pattern is the sharpened
threat here, so the corpus's frozen-ness must be impossible to miss. The wire
set deliberately does NOT carry the `live_only` marker (its presence on a frozen
payload would be a contradiction — a unit test asserts its absence), and carries
`frozen:true` + the freeze horizon `frozen_at` instead. The UI renders an
always-on `Badge` "Period-scoped · frozen as of YYYY-MM-DD" ABOVE the summary
(not buried in fine print), so an auditor can never mistake the period summary
for live state. The date is rendered as the UTC day portion for a stable,
locale-independent label; the full horizon is on the wire if a future surface
wants the instant.

### D3 — Audit-workspace mount: a card above the activity tabs in `ControlWorkspace`, frozen periods only. `high`

**Options considered.** (a) Inside `SamplePanel` (the Sampling tab), next to the
drawn sample. (b) Above the Sampling/Walkthrough/Comments tabs in
`ControlWorkspace`, gated on `period.status === "frozen"`.

**Chosen: (b).** `SamplePanel` is gated behind a created population + a pulled
sample, so a summary mounted there would be invisible until the auditor builds a
population — but the comprehension aid is most useful BEFORE that, when the
auditor first opens a control and wants the plain-language read of the frozen
evidence. `ControlWorkspace` already has `controlId` + the full `period`
(including `status` + `frozen_at`), so the mount is clean. Gating on
`status === "frozen"` mirrors the backend: an OPEN period has no frozen
population and the backend 409s (D4), so rendering the card for an open period
would only surface an error. The card sits above the tabs as a header-level
comprehension aid, consistent with 502's "Overview" placement on control-detail.

### D4 — An OPEN period is refused (409), not silently summarized over live state. `high`

**Options considered.** (a) For an open period, COALESCE the NULL `frozen_at` to
infinity (the slice-026/028 read-path convention) and summarize live state.
(b) Refuse an open period with a distinct `ErrPeriodNotFrozen` → 409 Conflict.

**Chosen: (b).** Option (a) is exactly the live/frozen mixing P0-749-1 forbids:
a "period-scoped" summary that silently became a live summary because the period
wasn't frozen yet is the audit-period-pollution failure in disguise. The
period-scoped summary is defined OVER the frozen sample population; an open
period has none, so the honest answer is "use the live control-detail summary
instead", surfaced as a 409 (mirroring the freeze endpoint's
already-frozen-state-mismatch shape). The frontend gates the card on
`status === "frozen"` so the 409 is not normally reached; the backend guard is
the load-bearing enforcement.

### D5 — The horizon-bound citation resolver is a SECOND gate behind the grounding gate. `medium`

**Context.** P0-749-1 requires that a post-freeze id "must not even be citable".
The grounding gate (`allowedIDs` over the frozen `EvidenceSet`) ALREADY enforces
this — a post-freeze record is absent from the frozen set, so its id is never in
`allowed`, so a draft citing it is suppressed before any DB call. That alone
satisfies P0-749-1.

**Decision.** I still made the citation resolver horizon-bound
(`ResolveBeforeHorizon` membership-tests the candidate id against the
frozen-population read, refusing a post-freeze id even though it is a real
tenant-owned row). Defense-in-depth: the two gates fail a post-freeze id
independently, and the integration test asserts BOTH (the resolver refuses the
post-freeze id directly, AND the end-to-end summary suppresses it). The control
check stays unbounded (the control catalog is not period-versioned in v1).

### D6 — Two additive sqlc queries, no migration. `medium`

The existing slice-028 frozen-sample read (`ListEvidenceForPeriodControl`)
returns `id, observed_at, result, hash` with no `evidence_kind` and no LIMIT —
not enough for the bounded top-N cited-excerpt corpus the summary needs. Rather
than overload that query, I added two queries that mirror 502's live
`ListEvidenceRecordsByControl` / `CountEvidenceRecordsByControl` exactly, plus
the `observed_at ≤ COALESCE(frozen_at, 'infinity')` horizon predicate (the same
predicate the slice-026/028 path uses). No schema change; `sqlc generate` (pinned
v1.31.1) emitted the dbx funcs cleanly.

## Inherited (re-affirmed) 502 calls

These are unchanged from slice 502 / 444 and re-affirmed here because they are
bindingness-independent and apply identically to the frozen surface:

- **Not persisted (no `ai_generations` row)** — regenerated on demand
  (P0-502-4). Same `runSummary` path; the surface enum stays
  `llm.SurfaceSummary`.
- **No approve/publish/export** — the value is read-only by construction; the
  wire `summaryWire` carries `binding:false` + a disclosure and no action field
  (P0-502-3). Unit + vitest tests assert the absence.
- **Validate-every-citation-then-suppress** — a single unresolvable / ungrounded
  / non-frozen citation suppresses the whole summary (P0-502-1 / P0-749-1).
- **Cross-tenant proven absent** — retrieval + resolution run under
  `app.current_tenant`; a Tenant-B summary cannot cite a Tenant-A record
  (AC-3, proven live).
- **Local-Ollama default** — rides the slice-499 per-tenant inference client;
  cloud only under opt-in + banner (P0-502-6).
- **Graceful degradation + bounded top-N** — the deterministic frozen list
  always renders; the corpus is capped at `MaxCitedExcerpts` (P0-502-7/8).

## Revisit once in use

1. **Period-aware control catalog.** The citation resolver's control check is
   unbounded because the control catalog is not period-versioned in v1. When
   framework-version-pinned control sets land (slice 484 line), revisit whether
   the period summary should resolve controls at the period's framework version.
2. **Frozen-set recency vs. coverage bound.** Like 502, the top-N is a recency
   bound (`observed_at DESC`). For a long frozen period a coverage-spread bound
   (sample across the window) might summarize better than "newest N". Defer until
   an operator reports the recency bound missing relevant frozen evidence.
3. **Reuse the frozen sample population proper.** This slice reads the control's
   frozen evidence directly (`observed_at ≤ frozen_at`). If/when a period has an
   attached `populations`/`samples` row, a future slice could summarize the
   ACTUAL drawn sample rather than the full frozen population — closer to what the
   auditor is annotating. Out of scope here (the slice doc scopes it to the
   frozen population).
4. **E2E un-comment.** The audit-workspace Playwright spec carries the
   period-summary contract assertions commented pending the slice-082 seed
   harness (a frozen period + in-window evidence is a precondition the
   docker-compose bootstrap cannot yet establish). Un-comment when 082 lands.
