# 182 — Board-narrative AI-assist foundation (CLAUDE.md expansion + tone reference + model-refresh cadence)

**Cluster:** Docs / AI-assist governance
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Canvas open question #14 resolved 2026-05-20: board-narrative AI-assist commits to a seven-decision bundle (hybrid input · per-section approval · full prompt+response audit trail · four guardrails · Llama 3.1 8B default · inline-edit rejection flow · prompt-version snapshot). Board-narrative implementation is **v2+ work** — no actual implementation ships from this slice. But several foundation pre-commitments need to land NOW so the eventual board-narrative v0 lands cleanly against a constitution that captures the seven decisions.

The CLAUDE.md "AI-assist boundary (hard)" section was already expanded as part of the OQ #14 resolution PR. This slice ships **the companion artifacts** that the CLAUDE.md expansion references:

- The full tone-anti-pattern reference list (CLAUDE.md mentions the abbreviated list; the canonical full list lives in this slice's deliverable)
- The local-model-recommendation refresh cadence documented in operator docs (the "every 6-12 months" maintainer task)
- The schema-level extension reference (which columns get added to board-narrative records when v0 ships)
- A canvas §4.6 (Evidence Engine — AI-assist) update reflecting the seven decisions
- An ADR capturing the rationale + the why-this-set-of-decisions (since board-narrative is high-risk and the rationale deserves an architecture decision record)

**WHAT this slice ships.**

1. **NEW file `docs/governance/board-narrative-tone-anti-patterns.md`** — the canonical full list of banned phrases / framings / voice patterns the system prompt will reject when board-narrative v0 ships. Living document; updated as the maintainer encounters new anti-patterns from real board-pack drafting.
2. **NEW section in `docs/operator/maintenance-cadence.md`** (or create the file if it doesn't exist) — the "default local model refresh" task: every 6-12 months, the maintainer evaluates the default local model recommendation (currently Llama 3.1 8B Instruct) and updates operator docs if a better-quality / same-resource model has shipped.
3. **`Plans/canvas/04-evidence-engine.md` update** — §4.6 (AI-assist) gets a board-narrative subsection reflecting the seven sub-decisions. Cross-links to CLAUDE.md "Board-narrative AI-assist" subsection + this slice's tone-anti-pattern reference.
4. **NEW ADR at `docs/adr/0002-board-narrative-ai-assist.md`** — the architecture decision record capturing: the deep insight (asymmetric hallucination cost), the seven decisions, the rationale per decision, the trade-offs accepted, the alternatives rejected.
5. **NO actual implementation.** No board-narrative endpoint. No prompt template files. No model integration. No schema migration. Foundation-only.

**SCOPE DISCIPLINE — what's deliberately out.**

- **Board-narrative v0 itself.** That's a multi-slice v2+ build (prompt construction + numeric-verification pipeline + section-shape enforcement + operator UI + audit-log integration). Foundation-only here.
- **Schema migrations for `prompt_version` / `model_name` / `model_version` / `model_provider` columns.** Those land WITH board-narrative v0. The CLAUDE.md expansion documents the contract; the schema work happens when there's a board-narrative record table to add columns to.
- **Numeric-verification library design.** The "every number must match the deterministic pre-computation" guardrail is concrete-but-not-yet-designed. The library lands with board-narrative v0.
- **Cloud LLM provider integration.** Existing infrastructure per slice 050 already supports cloud LLM opt-in per tenant; no changes here.
- **Actual local model benchmarking.** Maintainer evaluates Llama 3.1 8B at v0 implementation time against real board-pack drafting; this slice documents the cadence, not the benchmark results.

## Threat model

Pure documentation slice; minimal new threat surface.

**S — Spoofing.** None.

**T — Tampering.** The tone-anti-pattern reference list is part of the trust root for board-narrative safety. A malicious PR that removes "we are proud to report" from the ban list (allowing the LLM to use that phrase) would weaken board-narrative safety. Mitigation: file is at `docs/governance/`; any PR modifying it requires maintainer review under branch protection.

**R — Repudiation.** None new. The ADR carries a permanent record of the decision rationale.

**I — Information disclosure.** None — pure governance docs.

**D — Denial of service.** None.

**E — Elevation of privilege.** None.

**Verdict.** **CLEAN** — documentation slice; the tampering concern (T) is mitigated by branch protection + maintainer review.

## Acceptance criteria

### Tone anti-pattern reference list

- **AC-1.** NEW file `docs/governance/board-narrative-tone-anti-patterns.md` containing:
  - Header explaining the file's purpose: canonical reference list of phrases / framings / voice patterns that the system prompt will reject when board-narrative v0 ships
  - **Section 1: Banned phrases (exact-match)** — at least the eight phrases enumerated in CLAUDE.md ("we are proud to report", "exceeded expectations", "industry-leading", "best-in-class", "world-class", "robust" as filler, "leverage" as verb-replacement-for-use, any unprompted superlative)
  - **Section 2: Banned framings (pattern-match)** — categories rather than exact phrases (e.g., "unprompted positive framing where the data is neutral", "marketing voice", "passive-voice deflection of issues", "future-tense optimism without specific commitment")
  - **Section 3: Permitted phrases that are commonly mistaken as banned** — explicit allow-list for cases where banning would be over-restrictive (e.g., "robust" is OK when describing a specific control posture, e.g., "the change-management process is robust against unauthorized merges" — but NOT OK as filler, e.g., "we have a robust program")
  - **Section 4: Living-document discipline** — process for adding new anti-patterns as the maintainer encounters them from real board-pack drafting
- **AC-2.** At minimum 15 entries across sections 1-3 (real signal, not just the canonical eight).
- **AC-3.** File explicitly states it's the **reference** consulted by the system prompt + numeric-verification pipeline; modifications require maintainer review.

### Maintenance cadence doc

- **AC-4.** Update or create `docs/operator/maintenance-cadence.md` with a new section "Local model recommendation refresh" containing:
  - The cadence: every 6-12 months
  - The trigger metric: when a new local model with comparable hardware requirements (8-16GB GPU) demonstrably outperforms the current default on board-narrative-style tasks
  - The maintainer task: (a) benchmark candidate models against a held-out board-pack draft set; (b) update CLAUDE.md "Board-narrative AI-assist" default-model reference; (c) update operator docs; (d) record the refresh in `docs/audit-log/model-refresh-<YYYY>-<MM>.md`
  - The current default: Llama 3.1 8B Instruct (locked at OQ #14 resolution 2026-05-20)

### Canvas update

- **AC-5.** `Plans/canvas/04-evidence-engine.md` §4.6 (AI-assist) gains a "Board-narrative" subsection reflecting:
  - The seven sub-decisions (abbreviated; cross-link to CLAUDE.md for the full constitutional text)
  - The deep insight: asymmetric hallucination cost because board members are typically non-technical
  - Cross-link to `docs/adr/0002-board-narrative-ai-assist.md`
  - Cross-link to `docs/governance/board-narrative-tone-anti-patterns.md`

### Architecture Decision Record

- **AC-6.** NEW file `docs/adr/0002-board-narrative-ai-assist.md` (slot 0002 since 0001 was the FrameworkScope workflow ADR per OQ #19 resolution).
- **AC-7.** ADR follows the existing format (look at `docs/adr/0001-framework-scope-workflow.md` for shape): Context · Decision · Consequences · Alternatives Considered · References.
- **AC-8.** ADR's "Decision" section enumerates all seven sub-decisions with the rationale for each (per the canvas resolution block).
- **AC-9.** ADR's "Alternatives Considered" section explicitly captures the rejected options per sub-decision (raw input vs hybrid · per-narrative vs per-section · diff-only audit trail vs full · etc.). Each rejection with one-sentence rationale.

### Cross-link discipline

- **AC-10.** CLAUDE.md "Board-narrative AI-assist" subsection cross-links to: (a) `docs/governance/board-narrative-tone-anti-patterns.md`; (b) `docs/adr/0002-board-narrative-ai-assist.md`; (c) the canvas §4.6 board-narrative subsection.
- **AC-11.** All four artifacts (this slice doc + the tone reference + the ADR + the canvas update) cross-link to each other where appropriate.

### Documentation

- **AC-12.** CHANGELOG entry under `[Unreleased] / Added`: "Board-narrative AI-assist foundation pre-commitments — CLAUDE.md expansion (already shipped) + tone anti-pattern reference + ADR-0002 + canvas §4.6 update + model-refresh cadence (#182)."

## Constitutional invariants honored

- **AI-assist boundary (hard)** — this slice operationalizes the OQ #14 resolution by making the board-narrative-specific guardrails referenceable. The CLAUDE.md expansion is the primary commitment; this slice ships the companion artifacts.
- **Local-Ollama-first posture** — preserved; the model-refresh cadence is the operational discipline that keeps the local-first story honest as the model landscape evolves.
- **No audit-binding artifact without one-click human approval** — reinforced; the tone anti-patterns + numeric verification + section-shape enforcement collectively make the "approval" actually meaningful for board narratives.

## Canvas references

- `Plans/canvas/11-open-questions.md` #14 (resolved 2026-05-20)
- `Plans/canvas/04-evidence-engine.md` §4.6 (AI-assist) — to be updated by this slice
- `CLAUDE.md` "AI-assist boundary (hard)" → "Board-narrative AI-assist" subsection (already updated by the OQ #14 resolution PR)

## Dependencies

- **OQ #14 resolved** (this slice's filing PR) — prerequisite; the seven sub-decisions must be locked in canvas before this slice's documentation work has solid ground.
- **OQ #16 partially resolved** (slice 050) — local Ollama default + cloud opt-in. This slice extends with the model-refresh cadence.
- No code-slice dependencies.

## Anti-criteria (P0 — block merge)

- **P0-182-1.** Does NOT ship any board-narrative implementation. No prompt template files. No model integration code. No schema migrations. No board-narrative endpoint. Foundation-only.
- **P0-182-2.** Tone-anti-pattern reference list is REAL — at minimum 15 entries across sections 1-3 (not just the eight enumerated in CLAUDE.md). The point is to be a useful reference, not a placeholder.
- **P0-182-3.** Does NOT promise or commit to specific board-narrative implementation timing. v2+ stays v2+; the slice doesn't pull it forward.
- **P0-182-4.** Does NOT introduce schema migrations for `prompt_version` / `model_name` / `model_version` / `model_provider` columns. Those land WITH board-narrative v0. This slice documents the contract; the schema work happens when there's a board-narrative record table to extend.
- **P0-182-5.** ADR follows the existing format precisely (Context · Decision · Consequences · Alternatives Considered · References). Drift from the format is rejected at review.
- **P0-182-6.** Local-model default (Llama 3.1 8B Instruct) MUST be cross-referenced consistently across CLAUDE.md, ADR-0002, and `docs/operator/maintenance-cadence.md`. Drift between these three references is a rejected merge.
- **P0-182-7.** The "Permitted phrases" section (AC-1 Section 3) is real, not aspirational. At least three concrete examples where the ban could be misapplied + the correct interpretation.
- **P0-182-8.** Does NOT modify CLAUDE.md beyond what was already shipped in the OQ #14 resolution PR. The CLAUDE.md text is locked; this slice ships the supporting artifacts that CLAUDE.md references.
- **P0-182-9.** Living-document discipline (AC-1 Section 4) MUST specify how new anti-patterns are added — via PR with maintainer review under branch protection. Not by edit-and-forget.

## Skill mix (3-5)

1. **OSS governance / ADR authorship** — reference shape from `docs/adr/0001-framework-scope-workflow.md`
2. **Plain-English tone identification** — writing the tone-anti-pattern list requires distinguishing "marketing voice" from "factual statement" — this is editorial judgment, not engineering
3. **Cross-link / artifact-coordination discipline** — multiple files need to cross-link each other consistently; pre-merge sanity check

## Notes for the implementing agent

### The tone-anti-pattern list is the load-bearing deliverable

The other artifacts (cadence doc, canvas update, ADR) are mechanical follow-on documentation. The tone-anti-pattern list is the artifact that future board-narrative v0 actually consumes. Spend the bulk of the slice on getting this right.

Source material for the anti-pattern list (don't copy verbatim; use as reference for the kind of voice patterns to ban):

- The Annual Letter section of well-regarded technical reports (e.g., Buffer's transparency reports, GitLab's annual report) — measured + specific
- Anti-examples from typical vendor security marketing copy — exactly what we DON'T want
- The maintainer's own past board-pack drafts (if any exist) — measured + specific by definition

### ADR slot computation

OQ #19 resolution at slice 050 / FrameworkScope ADR landed at `docs/adr/0001-framework-scope-workflow.md`. This slice's ADR is `0002-board-narrative-ai-assist.md`. Verify the slot is still free at pickup time (`ls docs/adr/`); if a `0002-` ADR has landed in the meantime, increment.

### Cadence-doc creation vs update

`docs/operator/maintenance-cadence.md` may not exist yet. If it doesn't, create it with this slice as the seeding content; if it does, append a new section. Cross-check via `ls docs/operator/` at pickup.

### Spillover candidates

If during this slice an out-of-scope finding emerges:

- **Actual board-narrative v0 design grill** — file via `/idea-to-slice` as a separate v2 slice; do NOT roll into this slice
- **Numeric-verification library design** — separate v0-companion slice
- **System-prompt-template authoring** — separate v0-companion slice (locked-in tone anti-patterns inform it but the prompt template itself is implementation)
- **Cloud LLM opt-in UX** — already exists per slice 050; if gaps surface, file a separate UX slice

### Provenance

Filed 2026-05-20 as the foundation slice for the OQ #14 resolution. Same session that filed slices 178, 179, 180, 181 — all foundation/governance work for canvas open-question resolutions. Board-narrative v0 fires at v2+ when product surfaces in that priority order; this slice ensures the constitution + reference artifacts are in place by then.
