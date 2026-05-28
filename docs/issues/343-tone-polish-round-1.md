# 343 — Tone polish round 1 (low-density bundle from slice 337 audit)

**Cluster:** Docs
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** 337 (AI-writing tone audit)

## Narrative

Surfaced during slice 337 audit, captured as follow-up per continuous-batch policy.

The slice 337 audit produced 10 low-density findings spread across five files. None individually warrants its own rewrite slice; together they make sense as a single polish round.

## What ships in this slice

Per-file fixes per the audit's findings table (`docs/audits/337-ai-writing-auditor-report.md`):

### `README.md`

| Line  | Issue                                           | Fix                                                                                    |
| ----- | ----------------------------------------------- | -------------------------------------------------------------------------------------- |
| whole | 30 em-dashes in 1713 words (17.5 per 1000)      | Drop em-dashes that aren't load-bearing parentheticals; target ≤ 4 (≈ 2 per 1000).     |
| 200   | "first-pass" repeated twice in adjacent bullets | Vary the second occurrence: "scheduled review" or "scheduled audit cadence".           |
| 26    | "operator-grade today" (single Tier 3 marker)   | Optional — replace with a specific posture line if the bullet's intent is positioning. |

### `Plans/canvas/03-ucf.md`

| Line  | Issue                                   | Fix                                                          |
| ----- | --------------------------------------- | ------------------------------------------------------------ |
| whole | 5 em-dashes in 646 words (7.7 per 1000) | Drop two; the rest are load-bearing definitions. Target ≤ 1. |

### `docs/audit-log/340-chromedp-flake-decisions.md`

| Line(s)     | Issue                                       | Fix                                                                                                                                                          |
| ----------- | ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 36, 39, 137 | "load-bearing" used 3× in one decisions log | Optional — vary one occurrence ("primary fix", "central observation") to avoid wearing the term thin. The persona allows project jargon when used sparingly. |

### 10 recent `docs/issues/*.md`

| File        | Line(s)      | Issue                                      | Fix                                                                    |
| ----------- | ------------ | ------------------------------------------ | ---------------------------------------------------------------------- |
| 338-pentest | 92, 134, 184 | "Load-bearing" prefix on three subsections | Vary one ("primary objective" or "central probe"). Keep the other two. |

The "harness" references in slices 331/334/336 and the "load-bearing" references in slice 333 were audited and found to be in-bounds (project jargon used in a specific technical sense). No fix needed.

## Acceptance criteria

- [ ] **AC-1.** Em-dash counts in README.md and 03-ucf.md drop to within the audit's per-file target (README: ≤ 4; 03-ucf: ≤ 1).
- [ ] **AC-2.** README.md line 200's "first-pass" repetition is varied.
- [ ] **AC-3.** `docs/audit-log/340-chromedp-flake-decisions.md` has at most 2 occurrences of "load-bearing" (down from 3) OR a note that the third is genuinely load-bearing in the project sense.
- [ ] **AC-4.** `docs/issues/338-pentest-atlas-edge.md` has at most 2 subsections opening with "Load-bearing" (down from 3).
- [ ] **AC-5.** No other prose changes; the diff is bounded to the items above.
- [ ] **AC-6.** No factual, structural, or positioning claim is altered in any file.
- [ ] **AC-7.** `pre-commit run --files <each touched file>` passes.

## Constitutional invariants honored

- **AI-assist boundary (CLAUDE.md).** The tone discipline is part of this boundary.
- **Survive third-party security review (canvas §6).** Public-facing surfaces (README) stay measured.

## Canvas references

- `CLAUDE.md` "Tone discipline (banned phrases in the system prompt)"

## Dependencies

- **#337** (AI-writing tone audit) — parent; produces the audit report this slice acts on.
- **#342** (vision canvas tone rewrite) — sibling spillover. Either can land first.

## Anti-criteria (P0 — block merge)

- **P0-343-1.** Does NOT change the structural argument or positioning of any audited file.
- **P0-343-2.** Does NOT modify files outside the audit's bounded surface.
- **P0-343-3.** Does NOT expand scope to "fix all em-dashes everywhere" — bounded to the four files listed.
- **P0-343-4.** Does NOT modify `Plans/canvas/01-vision.md` — slice 342 owns that file.
- **P0-343-5.** Does NOT auto-merge.

## Skill mix

- Standard read/edit
- `voltagent-qa-sec:ai-writing-auditor` persona reference for em-dash triage

## Notes for the implementing agent

The audit report at `docs/audits/337-ai-writing-auditor-report.md` is the source of truth for the specific fixes. This slice is a bundle of low-density work that the maintainer can review as one PR.

**If any fix surfaces a structural question** (e.g. removing an em-dash forces a sentence rewrite that changes meaning), STOP the fix on that line, leave the em-dash, and note the finding in a decisions log for slice 342's scope or a future audit pass.
