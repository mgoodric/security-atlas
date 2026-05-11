# Plans/prompts — canonical build prompts

> Four-cadence prompt set for taking security-atlas v1 from decomposed backlog to shipped slices.

The build flow leans on three layers (see also [`../../CLAUDE.md`](../../CLAUDE.md)):

- **Layer 1 — context (auto-loaded):** `CLAUDE.md` carries constitutional invariants, locked tech stack, anti-patterns, AI-assist boundary, and quality gates. Every session inherits it.
- **Layer 2 — decomposition (already done):** the original `to-issues` decomposition produced 49 tracer-bullet slices in `docs/issues/`.
- **Layer 3 — per-slice execution:** each issue gets a short, copy-pasteable prompt that triggers Algorithm mode and the quality-gate sequence.

## The four prompts

| #   | File                                                     | When to use                                                                                                   | Modifies repo?                                       |
| --- | -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| 01  | [`01-backlog-review.md`](./01-backlog-review.md)         | Once, before any code lands. Produces `docs/issues/_REVIEW.md`.                                               | No (review only)                                     |
| 02  | [`02-github-sync.md`](./02-github-sync.md)               | Optional — when issues should live on github.com alongside the markdown.                                      | Creates GH issues, updates `_INDEX.md` Status column |
| 03  | [`03-execute-issue-001.md`](./03-execute-issue-001.md)   | The first real build session. Kicks off scaffolding (this is the CLAUDE.md "When code begins" approval gate). | Yes — opens PR for the monorepo skeleton             |
| 04  | [`04-per-slice-template.md`](./04-per-slice-template.md) | Every slice after 001. Includes a parallel-worktree variant for streams 2+.                                   | Yes — opens PR per slice                             |

## Cadence

```
[01 review] → fix P0/P1 findings → [optional 02 GH sync] →
[03 execute issue 001] → [04 per-slice for 002] → ... → v1 done
```

After slices 001 + 002 land (~day 4.5 by current estimates), up to 10 streams unlock in parallel per `docs/issues/_DEPENDENCY_GRAPH.md`. Realistic for a solo build: 1–2 sustained streams. Use the parallel-worktree variant in `04-per-slice-template.md` to run a second stream in `../security-atlas-NNN/` without filesystem conflicts.

## Report-back gates

Prompts 01 and 02 pause and print findings BEFORE writing files. Use this — redirecting on paper is cheaper than redirecting after disk changes.

## Style discipline

Every prompt requires Algorithm mode and cites CLAUDE.md quality gates by name. Don't strip the workflow steps (`grill-with-docs`, `tdd`, `database-designer`, `security-review`, `simplify`, `ship-gate`, `changelog-generator`) when adapting — they're the contract that keeps slices "polished end product" quality.

## Provenance

Generated 2026-05-10 during a PAI session. Companion vault note:
`02 - Areas/Technology/AI/PAI/PAI 5.0 Ecosystem - Project Profile - Security Program Manager.md`
