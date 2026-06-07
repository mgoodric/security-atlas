# 501 — Board-narrative full multi-section + numeric-claim verification library + banned-phrase enforcement wiring

**Cluster:** Board / AI-assist
**Estimate:** L (3d)
**Type:** JUDGMENT (numeric-extraction shape + section template set + banned-phrase match strictness)
**Status:** `blocked` (needs #498 — `internal/llm` substrate — and #440 — the one-section tracer-bullet)

> Filed 2026-06-06 via the AI-assist/reporting gap analysis. Slice 440 ships the
> board-narrative AI **tracer bullet — exactly ONE numbered section** with all
> seven guardrails wired for that one section, and explicitly names "remaining
> narrative sections" as a follow-on. Slice 182 (the foundation) states the
> banned-phrase list "must be wired into the LLM system prompt when
> board-narrative v0 ships" and that **"the v2 slice that introduces the call
> site owns wiring it in"** — and that the **numeric-verification library** is
> "concrete-but-not-yet-designed … lands with board-narrative v0." Slice 440
> wires both for one section; the **full multi-section narrative + the reusable
> numeric-verification library + the systematic banned-phrase enforcement across
> all sections** are the unowned remainder. This slice closes it.

## Narrative

**Why (the gap today).** After slice 440, the board narrative can AI-draft **one**
numbered section (proposed: control-coverage-summary) end-to-end with the seven
guardrails. A real board pack has multiple sections — risk posture, exception
status, audit-period progress, KPI movement, incidents — each with its own
deterministic pre-computation and its own numeric claims. Slice 440 proves the
machinery on one section; it does not scale it. Two pieces of machinery in
particular were proven once but not generalized: (a) the **numeric-claim
verification** that auto-rejects a draft whose numbers do not match the
deterministic pre-computation — slice 440 does this for one section's numbers,
but slice 182 calls for a reusable **numeric-verification library**; and (b) the
**banned-phrase tone enforcement** — slice 440 wires slice-182's list into one
section's system prompt; the full narrative needs it applied systematically
across every section.

**What (the deliverable shape).**

1. **Remaining numbered sections, end-to-end** — each additional board-narrative
   section gets its deterministic pre-computation rollup + hybrid prompt + the
   full seven-guardrail pipeline, reusing the slice-440 machinery. The section
   set is the JUDGMENT call (proposed: risk-posture, exception-status,
   audit-period-progress, KPI-movement — the rollup-grounded ones; freestyle
   commentary stays human-authored).
2. **A reusable numeric-claim verification library** — extract slice 440's
   one-section numeric check into a library that, given a draft + its
   deterministic pre-computation, finds every numeric claim in the text and
   auto-rejects the draft on any mismatch. Every section consumes it; new
   sections inherit it for free. This is the slice-182 "numeric-verification
   library" deliverable.
3. **Systematic banned-phrase enforcement wiring** — slice-182's tone
   anti-pattern list (`docs/governance/board-narrative-tone-anti-patterns.md`)
   is wired into the system prompt for **every** section + a post-generation
   banned-phrase check rejects a draft containing a banned phrase (exact-match
   for the listed phrases; the "permitted phrases" allow-list from slice 182
   Section 3 is honored so correct usages are not false-rejected). This is the
   slice-182 "v2 slice that introduces the call site owns wiring it in"
   commitment, generalized.
4. **Per-section approval across the assembled narrative** — the full narrative
   is the numbered set of sections; each is approve/edit/reject independently
   (slice-440 per-section discipline at narrative scale). The board pack ships
   only approved sections.

**Scope discipline.** This slice scales the **proven slice-440 machinery to the
full rollup-grounded section set**, extracts the **numeric-verification library**,
and generalizes the **banned-phrase enforcement**. It does **not** add new
guardrails (the seven are constitutional and already proven by 440). It does
**not** ship cloud routing (that is slice 499; this inherits whatever backend the
tenant is on). It does **not** auto-generate freestyle commentary sections (those
stay human-authored per the AI-assist boundary). **Follow-on slices:** scheduled
board report generation / distribution cadence (if demand surfaces — board packs
already support generation + freezing + history + PDF export per slice 031/032,
so this is a thin add, not a v1 gap); narrative-level diff between successive
board packs.

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — same highest-risk
family as slice 440 (board narratives reach a non-technical audience at face
value); the dominant threats are **hallucinated numbers / forged citations
across more sections** and **tone/persuasion drift**, plus the always-present
**cross-tenant bleed**.

**S — Spoofing.** Generation + approval require the board-report role (slice 440
gate).
_Mitigation/AC:_ all section generate/approve endpoints reuse slice 440's
role-gated path; approval records `human_approver`.

**T — Tampering / hallucination across sections (PRIMARY).** More sections = more
numeric claims + more citations the model could fabricate. A wrong number in a
board pack is the asymmetric-cost failure the whole bundle exists to prevent.
_Mitigation/AC:_ the reusable **numeric-verification library** checks every number
in every section against that section's deterministic pre-computation and
auto-rejects on mismatch **before the operator sees the draft**; citation
validation (slice-440) applies per section; section-shape enforcement applies per
section. The operator never sees a section that failed validation.

**R — Repudiation.** Each section's full prompt + inputs + draft + edits + final
must be reconstructable.
_Mitigation/AC:_ every section writes a slice-498 `ai_generations` audit row
(model name/version/provider + prompt version + full prompt + context + draft);
old rows immutable.

**I — Information disclosure / cross-tenant bleed.** Each section's
pre-computation + cited excerpts are tenant-confidential.
_Mitigation/AC:_ every section's rollup + excerpts are assembled under
`app.current_tenant` (RLS); every cited ID is asserted tenant-owned. An
integration test proves a tenant-B narrative cannot include a tenant-A excerpt or
citation in any section.

**D — Denial of service.** Generating many sections multiplies inference cost.
_Mitigation/AC:_ each section inherits the slice-498 mandatory token budget +
timeout; section generation is bounded (cited-excerpt cap per section, not the
full corpus); per-tenant rate limiting applies to the multi-section generate.

**E — Elevation of privilege.** AI must not publish a board pack without
per-section one-click human approval.
_Mitigation/AC:_ every section is draft-only until a human approves it (slice-498
`ai_assisted ↔ human_approver` enforcement); the board pack ships only approved
sections; no path auto-approves a section.

## Acceptance criteria

### Multi-section generation

- [ ] **AC-1.** Each additional rollup-grounded section (the chosen set) gets its
      deterministic pre-computation rollup + hybrid prompt + the full
      seven-guardrail pipeline, reusing slice-440 machinery.
- [ ] **AC-2.** The section set is documented in the decisions log with the
      rationale for which sections are AI-drafted vs human-authored
      (freestyle commentary stays human-authored).

### Numeric-claim verification library

- [ ] **AC-3.** A reusable numeric-verification library extracts every numeric
      claim from a draft and checks it against that section's deterministic
      pre-computation; any mismatch auto-rejects the draft before the operator
      sees it (the slice-182 "numeric-verification library").
- [ ] **AC-4.** Every section consumes the library; a unit test proves a
      fabricated number in any section auto-rejects.

### Banned-phrase enforcement wiring

- [ ] **AC-5.** Slice-182's tone anti-pattern list is wired into the system
      prompt for **every** section; a post-generation banned-phrase check rejects
      a draft containing a banned (exact-match) phrase.
- [ ] **AC-6.** The slice-182 Section-3 "permitted phrases" allow-list is honored
      — a correct usage (e.g., "robust against unauthorized merges") is NOT
      false-rejected; a unit test proves both the rejection and the allow-list.

### Per-section approval at narrative scale

- [ ] **AC-7.** The full narrative is the numbered set of sections; each is
      approve/edit/reject independently (slice-440 per-section discipline); the
      board pack ships only approved sections.
- [ ] **AC-8.** Every section writes a slice-498 `ai_generations` audit row;
      `ai_assisted=true ⇒ human_approver` holds per section.

### Tests

- [ ] **AC-9.** Integration test: a multi-section narrative generates, each
      section passes all guardrails, and reaches per-section draft state.
- [ ] **AC-10.** Unit test: the numeric-verification library rejects a fabricated
      number and accepts a correct one (across section shapes).
- [ ] **AC-11.** Unit test: the banned-phrase check rejects a banned phrase and
      honors the permitted-phrase allow-list (AC-6).
- [ ] **AC-12.** **Cross-tenant isolation test:** a tenant-B narrative cannot
      include a tenant-A excerpt or citation in any section.
- [ ] **AC-13.** Integration test: a board pack ships only approved sections;
      an unapproved section is excluded.

### Docs / JUDGMENT artifact

- [ ] **AC-14.** Decisions log
      (`docs/audit-log/501-board-narrative-multisection-decisions.md`): the
      section set, the numeric-extraction approach, the banned-phrase match
      strictness, and the "Revisit once in use" list.
- [ ] **AC-15.** Changelog + board-module docs note the full multi-section
      narrative + the numeric-verification library + banned-phrase enforcement.

## Constitutional invariants honored

- **Board-narrative AI-assist (load-bearing — seven decisions).** All seven
  guardrails applied across every AI-drafted section; the numeric-verification
  library + banned-phrase enforcement are the slice-182 commitments now wired in
  at the call site (slice 182: "the v2 slice that introduces the call site owns
  wiring it in").
- **AI-assist boundary (hard).** No board pack published without per-section
  one-click human approval; `ai_assisted ↔ human_approver`; full audit per
  section; never auto-approves; never seeds tenant B with tenant A data.
- **Tone discipline (banned phrases).** The slice-182 list is enforced at the
  system prompt + post-generation across all sections, honoring the permitted-
  phrase allow-list.
- **#6 RLS tenant isolation** — every section's assembly is per-tenant;
  cross-tenant bleed proven absent (AC-12).

## Canvas references

- `CLAUDE.md` "Board-narrative AI-assist (load-bearing — OQ #14 resolved
  2026-05-20)" — the seven decisions; the banned-phrase list; the
  numeric-claim-verification guardrail; "the v2 slice that introduces the call
  site owns wiring it in."
- `Plans/canvas/04-evidence-engine.md` §4.6.7 — board-narrative AI-assist; the
  asymmetric-hallucination-cost insight; the seven sub-decisions.
- `Plans/canvas/07-metrics.md` — board reporting first-class.
- `docs/adr/0006-board-narrative-ai-assist.md` — the board-narrative foundation
  ADR (per canvas §4.6.7 cross-link).

## Dependencies

- **#498 (this batch — `ready`/unbuilt)** — the `internal/llm` substrate + the
  `ai_generations` audit record + the `ai_assisted ↔ human_approver` enforcement.
  **Hard dependency.**
- **#440 (`ready`/unbuilt)** — the one-section board-narrative tracer bullet +
  the seven-guardrail machinery this slice scales + the one-section numeric check
  this slice extracts into a library. **Hard dependency — build 440 first.**
- **#182 (merged)** — the tone anti-pattern reference list this slice wires in +
  the documented numeric-verification-library + schema-extension contract.
- **#031 / #032 (merged)** — the board-pack data path + storage the sections
  assemble against (`internal/board`).

## Anti-criteria (P0 — block merge)

- **P0-501-1.** Does NOT publish a board pack without per-section one-click human
  approval; does NOT auto-approve any section.
- **P0-501-2.** Does NOT show the operator any section that failed numeric /
  citation / shape validation (guardrails are pre-operator).
- **P0-501-3.** Does NOT emit a banned-phrase / marketing-tone draft (slice-182
  list wired into every section's prompt + checked post-generation).
- **P0-501-4.** Does NOT false-reject a slice-182 Section-3 permitted phrase.
- **P0-501-5.** Does NOT include any tenant-A excerpt/citation in a tenant-B
  narrative section (cross-tenant — proven by AC-12).
- **P0-501-6.** Does NOT add new guardrails or weaken the seven — it scales the
  proven machinery to more sections.
- **P0-501-7.** Does NOT auto-draft freestyle commentary sections — those stay
  human-authored.
- **P0-501-8.** Does NOT let `ai_assisted=true` reach `human_approved=true`
  without `human_approver` (schema invariant) for any section.

## Skill mix (3-5)

- `grill-with-docs` — align the section set + numeric-verification shape with the
  canvas §4.6.7 seven decisions + slice-182.
- `tdd` — the numeric-verification library + banned-phrase check + cross-tenant
  tests are load-bearing.
- `security-review` — the highest-risk AI-assist surface (asymmetric
  hallucination cost) + tenant isolation.
- `simplify` — the numeric-verification library must be reusable + small enough
  that new sections inherit it cleanly.

## Notes for the implementing agent

- **The numeric-verification library is the load-bearing extraction.** Slice 440
  proves the numeric check for one section; your job is to make it a reusable
  library so every section — and every future section — inherits the
  auto-reject-on-mismatch guarantee. A wrong number in a board pack is the exact
  asymmetric-cost failure the whole seven-decision bundle exists to prevent.
- **Banned-phrase enforcement honors the allow-list.** Wire slice-182's list into
  every section's system prompt AND check post-generation, but respect the
  Section-3 permitted-phrase allow-list so "robust against unauthorized merges"
  (correct) is not false-rejected alongside "we have a robust program" (filler).
- **Build-ordering.** Needs slice 498 (substrate) + slice 440 (one-section
  machinery + the numeric check to extract). Confirm both have landed at pickup;
  until then this stays `blocked`.
- **Scheduling/distribution is NOT this slice.** Board packs already support
  generation + freezing + list/history + PDF export (slice 031/032). A scheduled-
  cadence add is a thin follow-on if demand surfaces, not a v1 gap — do not pull
  it in.
- **Registration note (slice-382).** This slice's `_STATUS.md` row is NOT
  registered on this `docs/501` branch; the orchestrator registers it via a
  `chore/status` action after the spec PR merges.
