# 344 — CLAUDE.md tone discipline expansion (additions from slice 337 audit)

**Cluster:** Docs
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** 337 (AI-writing tone audit)

## Narrative

Surfaced during slice 337 audit, captured as follow-up per continuous-batch policy.

The audit at `docs/audits/337-ai-writing-auditor-report.md` proposed three additions to `CLAUDE.md`'s "Tone discipline (banned phrases in the system prompt)" section. Per P0-337-6, slice 337 did NOT modify CLAUDE.md directly. This slice carries that change.

The three candidate additions are:

1. **"first-pass" — flag as repetition-prone.** The audit observed "first-pass" used twice in adjacent bullets in README.md line 200. Not a banned phrase; a candidate for a "vary terminology in adjacent occurrences" general rule.
2. **"load-bearing" — qualifier on use.** The project uses "load-bearing" as a meta-term for invariants and key decisions. The audit observed mild over-use (3× in one decisions log; 3× in one slice doc subsection). Candidate for a "use only when the claim is falsifiable" qualifier alongside the existing list.
3. **"harness" — documented exception.** The persona's Tier 2 list flags "harness" as an AI-ism. The project uses "harness" literally to name the slice 178 UI honesty harness. Candidate for documenting that the persona's Tier 2 list is supplementary and project-specific exceptions apply (with "harness" as the canonical example).

Additionally, the audit surfaced a forward-looking gap worth recording in CLAUDE.md:

4. **Board-narrative banned-phrase enforcement is documented but not yet runtime-enforced.** The v1 board narrative is template-only (`internal/board/narrative.go`). When slice 182's v2 work introduces an LLM call site, the banned-phrase list must be added to the system prompt at that time. Add a small forward-looking note to CLAUDE.md so the v2 implementer doesn't have to re-derive this.

## What ships in this slice

A single edit to `CLAUDE.md` that:

1. Adds a "Repetition discipline" sub-bullet to the tone-discipline section (covers points 1, 2 above as a generalizable rule rather than a phrase-by-phrase list).
2. Adds a "Project-specific exceptions" sub-paragraph that documents "harness" as a literal technical term, not the persona's AI-ism (covers point 3).
3. Adds a one-sentence forward-looking note at the end of the AI-assist boundary section (covers point 4) — to be honored by the slice that ships board-narrative-v0 LLM integration.

No other changes to CLAUDE.md.

## Acceptance criteria

- [ ] **AC-1.** `CLAUDE.md`'s tone-discipline section adds a "Repetition discipline" sub-bullet ("vary recurring terminology in adjacent occurrences; "load-bearing" is canonical jargon but flag if it appears 3+ times in one document").
- [ ] **AC-2.** `CLAUDE.md` adds a "Project-specific exceptions" note documenting "harness" as a literal technical term referencing slice 178's Playwright harness.
- [ ] **AC-3.** `CLAUDE.md`'s AI-assist boundary section adds a one-sentence forward-looking note: the board-narrative banned-phrase list must be wired into the LLM system prompt when slice 182's v2 work introduces the call site (no enforcement surface exists in v1).
- [ ] **AC-4.** The existing banned-phrase list (the bullet list under "Tone discipline (banned phrases in the system prompt)") is NOT shortened — additions only.
- [ ] **AC-5.** No other CLAUDE.md changes in this slice.
- [ ] **AC-6.** `pre-commit run --files CLAUDE.md` passes.

## Constitutional invariants honored

- **AI-assist boundary (CLAUDE.md).** This slice extends the discipline, never shrinks it (per P0-337-4 in the parent slice).
- **Survive third-party security review (canvas §6).** Public-facing CLAUDE.md stays measured.

## Canvas references

- `CLAUDE.md` "Tone discipline (banned phrases in the system prompt)"
- `CLAUDE.md` "AI-assist boundary (hard)" — board-narrative AI-assist subsection
- `docs/audits/337-ai-writing-auditor-report.md` "Candidate additions to CLAUDE.md tone discipline"

## Dependencies

- **#337** (AI-writing tone audit) — parent; produces the audit report with the proposed additions.
- **#182** (board-narrative AI-assist foundation) — `merged`. The forward-looking note in AC-3 references this slice's eventual v2 continuation.

## Anti-criteria (P0 — block merge)

- **P0-344-1.** Does NOT shrink the existing banned-phrase list. Additions only.
- **P0-344-2.** Does NOT modify any file other than `CLAUDE.md`.
- **P0-344-3.** Does NOT propose changes to runtime LLM-prompt code — slice 182's v2 work owns the enforcement surface when that slice arrives. This slice only documents the forward expectation.
- **P0-344-4.** Does NOT auto-merge — CLAUDE.md is the project's constitutional document; maintainer review required.
- **P0-344-5.** Does NOT introduce a new section to CLAUDE.md. The three additions land as sub-points inside existing sections.

## Skill mix

- Standard read/edit (CLAUDE.md only)

## Notes for the implementing agent

CLAUDE.md is the project's constitutional document. Edits are minimal-diff additions, not restructurings.

**Suggested wording for AC-1 (Repetition discipline sub-bullet):**

> - Repetition discipline: vary recurring terminology in adjacent occurrences. The project's domain jargon ("load-bearing", "first-pass", "tracer-bullet", "diligence the diligence tool") is canonical, but flag if any single term appears 3+ times in one document — the term is wearing thin.

**Suggested wording for AC-2 (Project-specific exceptions paragraph):**

> Project-specific exceptions: the persona's Tier 2 list flags some words as AI-isms that the project uses literally. The canonical example is "harness" — slice 178 names the UI honesty audit harness, and downstream slices reference it by that name. Do not rewrite these literal references; the persona's list is supplementary, this project's list is canonical.

**Suggested wording for AC-3 (forward-looking note in AI-assist boundary):**

> Forward note: the banned-phrase list above must be wired into the LLM system prompt when board-narrative v0 ships (slice 182's v2 continuation). No enforcement surface exists in v1 — the v1 board narrative is template-only (`internal/board/narrative.go`).

The implementing agent may adjust wording for fit; the substance is the load-bearing part.

**Audit log filename (this is a JUDGMENT slice):**
`docs/audit-log/344-claude-md-tone-discipline-expansion-decisions.md`
