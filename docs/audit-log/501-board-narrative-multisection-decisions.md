# Slice 501 — Board-narrative full multi-section + numeric-verification library + banned-phrase enforcement (decisions log)

JUDGMENT slice. This is the platform's **highest-risk AI-assist surface** — an
AI-drafted board-report narrative consumed by non-technical board members who take
the output at face value, so the hallucination cost is asymmetric. Slice 440
proved the four-gate pipeline for ONE section; this slice **scales the proven
machinery** to the rollup-grounded section set, **extracts the reusable
numeric-verification library**, and **generalizes the banned-phrase enforcement**.
It adds NO new guardrails and weakens none (P0-501-6). The runtime **AI-assist
boundary is constitutional and untouched** — this log is a development-process
artifact, not a relaxation of that boundary. The product still never publishes a
board-binding artifact without one-click human approval, never fabricates coverage
or numbers, and never seeds Tenant B with Tenant A data.

- detection_tier_actual: integration
- detection_tier_target: integration

> One Go-regexp incompatibility surfaced during the slice and was caught at the
> right tier (`target=integration, actual=unit`, package init panic on first
> test run): the carve-out filler patterns were first written with RE2-unsupported
> negative lookahead `(?!…)`. Go's `regexp` rejects this at `MustCompile` time, so
> the package failed to load in the routewalk unit test immediately — exactly
> where a malformed compile-time constant should surface, not in production. Fixed
> by rewriting the filler patterns without lookaround (each enumerates the specific
> illegitimate `<word> <abstract-noun>` constructions). No guardrail-bypass bug
> surfaced; no production data-path change.

---

## D1 — The chosen section SET (AC-2): which sections are AI-drafted vs human-authored

The board narrative is the numbered set of sections; each is approve/edit/reject
independently. The JUDGMENT call is **which sections an LLM may draft**. The
governing principle: a section is AI-draftable **iff every claim it makes is a
number the deterministic `board.Brief` pre-computation produced** (so the numeric
library can pin every claim) AND its supporting references are tenant-owned
citable ids. A section whose value is **subjective commentary or forward-looking
intent** stays human-authored — the LLM cannot ground those and the
asymmetric-hallucination cost is highest exactly there.

### AI-DRAFTED (rollup-grounded — every number is `board.Brief` ground truth)

| Section key                | Grounds on                                | Numbers it may state                                                    |
| -------------------------- | ----------------------------------------- | ----------------------------------------------------------------------- |
| `control_coverage_summary` | `Brief.Frameworks` (slice 440, unchanged) | coverage %, freshness %, 30-day drift delta/flipped-out, fw count       |
| `risk_posture_summary`     | `Brief.TopRisks`                          | open-risk count, worst residual severity (rounded int), oldest age      |
| `drift_activity_summary`   | `Brief.Drift`                             | drift window days, net delta (magnitude + signed), controls drifted out |

All three project the SAME frozen `board.Brief` the templated board pack already
consumes (slice 031) — **no new source-of-truth read, no invented number**. The
spec proposed "risk-posture, exception-status, audit-period-progress,
KPI-movement". I shipped coverage + risk-posture + drift-activity and folded
"audit-period-progress / KPI-movement" into the **drift-activity** section
(control drift over the period IS the KPI-movement / period-progress signal the
`Brief.Drift` summary already computes). I did **not** ship a separate
**exception-status** section: exceptions are not in `board.Brief` today, so a
rollup-grounded exception section would require a NEW read path (an exceptions
aggregate the brief does not assemble) — that is net-new source plumbing, out of
this slice's "scale the proven machinery / invent no number" discipline. It is
captured as a NAMED follow-on (see "Revisit once in use").

### HUMAN-AUTHORED (NOT auto-drafted — P0-501-7)

Freestyle commentary, **asks of the board**, the investment-vs-coverage
narrative, and operational color stay human-authored in the existing slice-032
templated board pack. The LLM never drafts these: they are subjective / forward-
looking / commitment-bearing, exactly the claims the asymmetric-hallucination
boundary forbids the model from inventing.

### Why the section abstraction (not three copies of the slice-440 path)

Each AI-drafted section is a `SectionDef` (`sections.go`): heading + item count +
rollup-builder + system-prompt + user-prompt. The Service is now
section-agnostic (`GenerateSection` / `GenerateAll`); the slice-440 `Generate`
is a one-line wrapper (`GenerateSection(ctx, SectionControlCoverage, …)`). This
is the `simplify` discipline: a new rollup-grounded section is a new `SectionDef`
entry + its rollup projection — it inherits the full four-gate pipeline, the
numeric library, the banned-phrase check, the per-section approval, and the audit
row for free, with zero pipeline change.

---

## D2 — The reusable numeric-claim verification library (AC-3): extraction + strictness

Slice 440's `verifyNumbers(text, rollup)` was extracted into the section-agnostic
**`VerifyNumbers(text string, allowed map[int]bool, periodEnd string) bool`**
(`numeric.go`). It depends on NOTHING section-specific — just the
`(text, allowed-set, period-end)` triple — so every section (and every future
section) consumes the identical auto-reject-on-mismatch logic. The slice-440 call
site is now a thin wrapper that supplies the coverage rollup's `AllowedNumbers()`.

**How it finds numeric claims in free text.** Regex `-?\d+` extracts every
integer token (optional leading minus). Before extraction it strips the three
classes of legitimate number-shaped tokens that are NOT statistics:

1. leading numbered-list markers (`1. `, `2. `, …) — the section template's
   structure, validated by the shape gate, not claims;
2. cited UUIDs — their hex/digit runs are ids, validated by the citation gate;
3. the EXACT period-end label (`stripLabelDate`) — a date the model invented is
   NOT stripped and therefore fails (the model must not invent dates either).

**Match strictness (the JUDGMENT call).**

- **Integers only.** Every section's ground truth is integers (rounded
  percentages, counts, rounded severities). A decimal in the draft is itself a
  fabrication signal — the model invented precision the pre-computation does not
  have — so `84.5` parses to `84` then `5`; `5` is not in a coverage section's
  allowed set, so the decimal draft is rejected. Intentional strictness.
- **Per-section allowed set.** `Rollup.AllowedNumbers()` is section-aware: the
  risk section's set is `{risk_count, worst_residual_severity, oldest_age}`; the
  drift section's is `{window_days, |delta|, signed_delta, flipped_out}`; the
  coverage section's is the unchanged slice-440 set. A number valid in one section
  is NOT automatically valid in another — the model cannot launder a coverage
  number into the risk section.
- **All-or-nothing.** A SINGLE number outside the section's allowed set fails the
  WHOLE section's draft. A board narrative with one fabricated statistic is
  unacceptable: the board cannot tell the fabricated number from the real ones.
- **Magnitude + signed delta.** A drift delta of `-3` may be written "net drift
  was -3" or "3 controls drifted out" — both honest renderings of the same ground
  truth — so both the magnitude `3` and the signed `-3` are allowed; the sign word
  is governed by the prose + shape, the digit is pinned to ground truth.

A unit test (`TestVerifyNumbers_Library`) proves a fabricated number auto-rejects
AND a correct one passes across coverage / risk / drift section shapes (AC-10);
an integration test proves a fabricated number in ONE section suppresses ONLY
that section while the others still draft (AC-4).

---

## D3 — Banned-phrase enforcement strictness + how the allow-list is honored (AC-5/AC-6)

Slice-182's tone list is wired into EVERY section's system prompt (each
`SectionDef.systemPrompt()` embeds `BannedPhraseListForPrompt()`; a unit test
asserts every section's prompt contains the list — `TestSectionDefs_AllWired`).
The post-generation `containsBannedPhrase` check (`tone.go`) is the deterministic
safety net, generalized across sections (it is section-agnostic — one list).

**Two layers of strictness:**

1. **Section-1 unambiguous list — exact case-insensitive `Contains`.** Phrases
   with no legitimate use in a board narrative ("we are proud to report",
   "industry-leading", "world-class", "unprecedented", …). No false-positive
   risk, so a plain substring match. (Unchanged from slice 440.)

2. **Section-3 context-sensitive words — filler-form match, allow-list honored.**
   Words like "robust" / "leverage" / "strong" / "mature" / "comprehensive" have
   BOTH a banned filler form and a permitted form. For these the check rejects
   ONLY the **filler form** and never the permitted form (P0-501-4). The reject
   logic: the word triggers a rejection iff a FILLER-form regex matches; the
   permitted forms (the Section-3 "OK when…" column) are the implicit complement
   and are never matched. Concretely:

   | word          | filler (rejected)                         | permitted (NOT rejected — allow-list)                       |
   | ------------- | ----------------------------------------- | ----------------------------------------------------------- |
   | robust        | `robust program/controls/posture/…`       | "robust **against** unauthorized merges"                    |
   | leverage      | `leverage the/our/its/… <noun>`           | (noun "leverage", rare in domain)                           |
   | strong        | `strong security/commitment/posture/…`    | "strong … (94% vs 88% within window)" (quantified)          |
   | mature        | `mature (security) program/posture/…`     | a cited maturity tier ("CMMI level 2 → 3")                  |
   | comprehensive | `comprehensive solution/program/approach` | "comprehensive **coverage of** 1403 controls" (cited scope) |

   This is the deliberate "exact-match the phrases that can never be right;
   pattern-match only the illegitimate filler constructions of the context-
   sensitive words; instruct + human-review the residue" division of labor from
   slice 182. **Go's RE2 has no lookaround**, so the filler patterns are written
   WITHOUT negative lookahead — each enumerates the specific illegitimate
   constructions, so a permitted usage never matches.

A unit test (`TestContainsBannedPhrase_AndAllowList`) proves BOTH the rejection
of Section-1 phrases + Section-3 filler forms AND the allow-list (the permitted
forms pass) — AC-11 / P0-501-3 / P0-501-4.

---

## D4 — Per-section approval at narrative scale + how the board pack ships only approved sections (AC-7/AC-13)

The slice-440 per-section approval discipline is unchanged: each section is its
own `board_narrative_sections` row (keyed `(tenant, period_end, section_key)`),
`ai_assisted=TRUE` / `human_approved=FALSE` until a separate one-click
`Approve` records the `human_approver` and flips `human_approved=TRUE`. The shared
slice-498 DB CHECK (`ai_assist_human_approver_guard`) makes
`ai_assisted=TRUE ⇒ (human_approved ⇒ human_approver present)` impossible to
violate at the DB layer for ANY section (P0-501-8). Every section writes a
slice-498 `ai_generations` audit row at generation time (P0-501-1 — no
auto-approve path exists).

**Board pack ships only approved sections (AC-13).** The new
`Store.ApprovedNarrative(ctx, periodEnd)` (`ListApprovedBoardNarrativeSections`
query, `human_approved = TRUE` under RLS) is the canonical assembly read: an
unapproved section — or a suppressed section that was never persisted — is
**structurally absent** from the result, so the assembled narrative can never
include it. An integration test approves two of three sections and proves the
third (unapproved) is excluded.

**Scope call — why the slice-032 templated pack is not restructured.** The
slice-032 quarterly board pack is a distinct artifact with its own FIXED section
set, templated text, and publish-gate. The slice-501 AI narrative is a separate,
higher-risk surface. AC-13 is satisfied at the boardnarrative layer
(`ApprovedNarrative` excludes the unapproved), which is the minimal, honest
surface consistent with the spec's "keep scope tight — backend-machinery-heavy"
discipline. Physically merging AI sections into the slice-032 pack's fixed-section
model + its publish lifecycle would be a larger, riskier restructure of a shipped
artifact and is out of scope; the two remain distinct artifacts.

---

## D5 — Local-Ollama default (P0-501 inheritance)

Every section's generation routes through the slice-499 per-tenant inference
client (`Server.inferenceClient()`, wired into the Service in
`register_board.go`, unchanged from slice 440). The default backend is local
Ollama; cloud is opt-in per tenant under `app.current_tenant` with the visible
routing banner (`SectionResult.CloudRouted` set from the resolved provider). No
section adds a cloud path; the multi-section generate inherits "no cloud by
default" + the banner for free.

---

## Revisit once in use (named follow-ons — NOT built here)

These were surfaced during slice 501 and are explicitly out of scope. Each is a
named follow-on per the continuous-batch policy:

1. **Exception-status AI-drafted section.** Requires a new exceptions aggregate
   in `board.Brief` (exceptions are not in the brief today). When that read lands,
   add a `SectionExceptionStatus` `SectionDef` — it inherits the full pipeline.
2. **Scheduled board-report generation / distribution cadence.** Board packs
   already support generation + freezing + history + PDF export (slice 031/032);
   a scheduled-cadence add is a thin follow-on if demand surfaces, not a v1 gap.
3. **Narrative-level diff between successive board packs.** A "what changed since
   last quarter's narrative" view — useful once operators have a generation
   history to diff. Out of scope here (no v1 demand signal yet).
