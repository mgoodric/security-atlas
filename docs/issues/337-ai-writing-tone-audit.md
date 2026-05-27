# 337 — AI-writing tone audit via voltagent-qa-sec:ai-writing-auditor

**Cluster:** Docs
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Runs `voltagent-qa-sec:ai-writing-auditor` against the project's
written surfaces to identify "AI-writing patterns" — generic praise,
unprompted superlatives, marketing-y framing, hedging language, and
the specific banned-phrase list maintained in CLAUDE.md's
board-narrative tone discipline section.

The project has codified a tone discipline (CLAUDE.md "Tone
discipline (banned phrases in the system prompt)") for one specific
surface: AI-generated board narratives. The discipline is broader
than just board narratives — the entire repo (canvas, docs, README,
slice text) is read by operators evaluating the platform, and
AI-writing-pattern drift across these surfaces degrades the
diligence-the-diligence-tool credibility just as much as a stale
README.

**Audit surface.** Tone audit across:

- **`Plans/canvas/*.md`** — the system-of-record design docs.
  Should be measured, factual, slightly defensive.
- **`docs/**/\*.md`\*\* — the docs site (mkdocs Material per slice 058) + audit-log + ADRs. Mixed audience: contributors + operators.
- **`README.md`** — public-facing top-of-funnel artifact.
- **AI-assist runtime prompts** — the LLM system prompts that drive
  AI-assist features (board narratives per slice 182, questionnaire
  drafting, etc.). These are the prompts the audit was originally
  scoped for — verify the banned-phrase list is actually enforced.
- **AI-assist suggestion surfaces** — any UI text that displays
  AI-generated content (`<aside>` banners, citation footers, gap
  explanations).
- **Slice text** (`docs/issues/*.md`) — meta: the slices that
  describe AI-assist work should themselves follow the tone
  discipline.

**Why now:** the banned-phrase list (CLAUDE.md) was codified
2026-05-20 (slice 182). The list explicitly says "the LLM voice for
board narratives is measured, factual, slightly defensive" — but
the discipline only applied to LLM-generated text at the time. With
~250 slices merged + a growing canvas, the audit asks: has the
same discipline held across Claude-authored repo content
(slices / decisions logs / drift blocks / canvas edits / README
refreshes)?

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 11/12.

**Disposition:** read-only tone audit + follow-up-slice fan-out.

## Threat model

Tone-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only — no edits to audited surfaces in
  this slice. AC enforces.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/337-ai-writing-tone-audit-decisions.md`.
- **I (Information disclosure):** Tone-finding details quote
  specific document fragments. All audited content is part of the
  OSS repo. CLEAN.
- **D (Denial of service):** CLEAN.
- **E (Elevation of privilege):** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:ai-writing-auditor` agent
      runs against the six surface categories in the narrative,
      using the CLAUDE.md banned-phrase list as the primary
      reference.
- [ ] **AC-2.** Findings recorded in
      `docs/audit-log/337-ai-writing-tone-audit-decisions.md`
      per surface: file path · line range · offending phrase ·
      banned-phrase-list category (or "novel pattern" if not on
      the list) · suggested rewrite.
- [ ] **AC-3.** **High-density findings** (a single document with
      many violations — e.g. a slice's narrative with 6+ "industry-
      leading"-style phrases) fan out as individual document-
      rewrite slices via `/idea-to-slice`.
- [ ] **AC-4.** **Low-density findings** (one phrase per document
      across many docs) bundle into a single "tone cleanup round 1"
      slice with a per-file diff.
- [ ] **AC-5.** **The audit explicitly visits the AI-assist
      runtime prompts** (per slice 182's tone reference document)
      and confirms that the banned-phrase list is actually
      enforced (not just documented). If enforcement is missing,
      file as a separate slice.
- [ ] **AC-6.** **Novel patterns** (AI-writing tells that aren't
      on the CLAUDE.md list but should be) get proposed as
      additions to the list, filed as a `Plans/prompts/...` /
      CLAUDE.md update slice via `/idea-to-slice`.
- [ ] **AC-7.** No surface text modified in this slice. Diff = doc
      files only (this slice + \_STATUS.md + decisions log).
- [ ] **AC-8.** Cross-references slice 182 (board-narrative AI-
      assist foundation) — the tone-anti-pattern reference
      document is the canonical list.
- [ ] **AC-9.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **AI-assist boundary (CLAUDE.md).** Tone discipline is a
  literal extension — the "banned phrases" list applies broader
  than originally scoped.
- **Survive third-party security review (canvas §6).** Tone
  drift on public-facing docs (README, canvas) erodes trust on
  the same axis as a stale README.

## Canvas references

- `Plans/canvas/01-vision.md` §6 — survive third-party review
- `CLAUDE.md` "Tone discipline (banned phrases in the system
  prompt)" — the canonical list

## Dependencies

- **#182** (board-narrative AI-assist foundation) — `merged`.
  Provides the tone-anti-pattern reference document.

## Anti-criteria (P0 — block merge)

- **P0-337-1.** Does NOT modify any audited surface (canvas /
  docs / README / prompts / UI) in this slice. Findings are
  filed as follow-up slices.
- **P0-337-2.** Does NOT bundle high-density findings into the
  low-density bundle. Tracer-bullet for high-density.
- **P0-337-3.** Does NOT auto-merge.
- **P0-337-4.** Does NOT propose lowering the banned-phrase
  discipline. The list grows; it doesn't shrink in this slice.
- **P0-337-5.** Does NOT include AI-writing patterns in the slice
  itself or in the decisions log (meta-discipline).
- **P0-337-6.** Does NOT touch CLAUDE.md to add novel patterns
  directly — that's a follow-up slice's content.
- **P0-337-7.** Does NOT auto-rewrite text "to fix tone" — every
  rewrite is a human-reviewed slice.

## Skill mix

- `voltagent-qa-sec:ai-writing-auditor` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Standard grep — pattern search against the banned-phrase list

## Notes for the implementing agent

**Banned-phrase list reference (subset from CLAUDE.md; full list at
slice 182's reference doc):**

- "we are proud to report"
- "exceeded expectations"
- "industry-leading"
- "best-in-class"
- "world-class"
- "robust" (when used as filler, not a specific control posture)
- "leverage" (as a verb, when "use" works)
- any unprompted superlative

**Search strategy.** Start with a literal grep across audited
surfaces, then progress to pattern-based matches (e.g.
"<adjective>-leading", "industry-<adjective>" templates).

**High-density vs low-density threshold (suggestion).** ≥3
violations in one document = high-density (warrants its own slice).
Otherwise = low-density (bundle).

**Meta-discipline (P0-337-5).** This slice's narrative + decisions
log themselves should be free of AI-writing patterns. If the
implementing agent catches itself writing "this slice provides
industry-leading tone discipline", that's an anti-pattern; rewrite
to the measured-factual voice CLAUDE.md mandates.

**Cross-reference protocol.** Findings in slice text that touch the
auth-substrate-v2 era (slices 187-198) likely overlap with the
broader AI-content review; note the cross-reference and let the
maintainer decide ownership at follow-up filing.

**Audit log filename:**
`docs/audit-log/337-ai-writing-tone-audit-decisions.md`
