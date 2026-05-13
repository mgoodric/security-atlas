# 04 — Per-Slice Template (issues 002 onward)

The canonical prompt shape for every slice **after** 001. Replace the **bolded** parts; don't change the workflow steps.

## Prompt template

```
Build docs/issues/<NNN>-<slug>.md.

HARD RULE — DO NOT STALL. The slice is not done until the PR is open on GitHub. If you find yourself drafting a "final report" or returning grill-with-docs output as a deliverable instead of executing the implementation, STOP drafting and finish the workflow. Commit + push + open PR + flip docs/issues/_STATUS.md row <NNN> from `in-progress` to `in-review` are non-optional steps, not appendices. The grill is a gate (pass through, do not return), not a deliverable.

Branch: <cluster>/<NNN>-<slug> (from main).

Workflow:
1. Read CLAUDE.md constitutional invariants + the issue's "Constitutional invariants honored" + "Canvas references" sections
2. grill-with-docs the issue against the cited canvas section(s) — flag drift before coding. After completing the grill, immediately proceed to step 3 in the same agent turn. Do NOT return to caller after the grill.
3. tdd loop per acceptance criterion (integration > unit; never mock the DB; never test private methods)
4. database-designer for any migration (idempotent + reversible)
5. security-review on any PR touching auth, ingest, RLS queries, or external IO
6. simplify pass before opening PR
7. ship-gate must pass before claiming done
8. changelog-generator entry for the slice
9. Status flip — append a `chore(status): <NNN> -> in-review` commit to the slice branch flipping `_STATUS.md` row <NNN>, push to the PR branch

Honor every anti-criterion (P0 — block merge).

Respect CLAUDE.md style (no emojis, Conventional Commits, Co-Authored-By trailer, DCO sign-off via `git commit -s`).

Open PR titled "feat(<cluster>): <short title> (#<NNN>)" with body: AC pass/fail · files changed · CI URL · open questions surfaced.

Use Algorithm mode. Initialize a PRD (id: <NNN>-<slug>).
```

## Substitution table

| Placeholder     | Example for issue 002                    | Notes                                                                                                                                      |
| --------------- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `<NNN>`         | `002`                                    | Always zero-padded 3 digits                                                                                                                |
| `<slug>`        | `schema-migrations`                      | Match the filename slug exactly                                                                                                            |
| `<cluster>`     | `spine`                                  | One of: spine, catalog, audit, board, control-as-code, evidence-pipeline, scope, risk, policies, vendor, auth, infra, frontend, connectors |
| `<short title>` | `schema + migrations for six primitives` | The H1 from the issue file, minus the leading number                                                                                       |

## Parallel execution variant

Use after slice 002 lands and you want a second concurrent stream. Run this in your **main** worktree first to set up the new worktree, then enter the worktree and run the per-slice prompt above.

```
Set up a parallel worktree for issue <NNN>.

git worktree add ../security-atlas-<NNN> -b <cluster>/<NNN>-<slug> main

Then, inside the worktree, run the per-slice template above for issue <NNN>. Stay inside the worktree — do NOT touch the main worktree or any other worktree's branch.

Use Algorithm mode. Initialize a PRD (id: <NNN>-<slug>-worktree).
```

## When to use which workflow step

Not every slice needs every step. Skip steps that don't apply:

| Step                  | Skip when                                                           |
| --------------------- | ------------------------------------------------------------------- |
| `grill-with-docs`     | Never skip — every slice cites canvas sections                      |
| `tdd`                 | Slice ships pure config or docs (rare; AC-1+ already implies tests) |
| `database-designer`   | Slice has no migration                                              |
| `security-review`     | Slice touches no auth, ingest, RLS, or external IO (rare)           |
| `simplify`            | Never skip — pre-PR quality gate                                    |
| `ship-gate`           | Never skip — definition of "done"                                   |
| `changelog-generator` | Slice is purely internal infrastructure (still recommended)         |

## After merge

- Squash-merge to main with a rewritten Conventional Commit message (per CLAUDE.md branching rules)
- Mark the issue Status as "Done · gh#NNN merged" in `_INDEX.md` (or via GH MCP if you ran prompt 02)
- If the slice unblocks new parallel streams, note them in your session log

## Quality gate failure modes

| Gate                | Failure mode                                                          | Action                                                                                                                                                                                                                                                                             |
| ------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `grill-with-docs`   | Canvas drift surfaced (e.g., the issue's AC contradicts an invariant) | STOP. Update the issue's AC or update the canvas — never paper over the drift                                                                                                                                                                                                      |
| `grill-with-docs`   | **Agent returns grill output as a "final report" without executing**  | **Agent stall — orchestrator resumes with explicit "execute now" directive, OR closes out the work directly (per `Plans/prompts/05-parallel-batch.md` Failure-mode playbook).** Observed in slice 061 (batch 15) and slice 028 (batch 16). The grill is a gate, not a deliverable. |
| `tdd`               | Test passes but mocks the DB or tests private method                  | Reject the test. Re-write to hit a real DB through the public API                                                                                                                                                                                                                  |
| `database-designer` | Migration is destructive or non-reversible                            | Re-design as additive; backfill in a separate slice                                                                                                                                                                                                                                |
| `security-review`   | Secret in code, broad IAM, missing RLS context                        | P0. Block merge until fixed                                                                                                                                                                                                                                                        |
| `ship-gate`         | Critical findings present                                             | P0. Block merge until cleared                                                                                                                                                                                                                                                      |

## Notes

- The per-slice prompt is intentionally short. CLAUDE.md does the heavy lifting — quality gates, invariants, anti-patterns, style.
- Don't add scaffolding-approval clauses to per-slice prompts; that gate was passed by issue 001.
- If a slice surfaces a new architectural decision, write an ADR in `docs/adr/NNNN-title.md` as part of the slice (per CLAUDE.md documentation discipline).
- If an open question from `Plans/canvas/11-open-questions.md` blocks the slice, STOP and resolve the question first. Don't ship a guess.

## Why the HARD RULE preamble exists

The anti-stall callout at the top of the prompt was promoted from a buried orchestrator note (`Plans/prompts/05-parallel-batch.md`) after the same failure mode landed twice:

- **Slice 061 (batch 15, CI path filter):** engineer returned 10-question grill analysis as final report. Resumed once with "execute now" → completed cleanly.
- **Slice 028 (batch 16, AuditPeriod + freezing):** engineer returned ADR draft + canvas reconciliation as final report. Resumed once with "execute now" → completed cleanly.

Both engineers were doing high-quality grill work — they just treated the grill as a deliverable instead of a gate. The fix is to make the rule the literal first line of the prompt body and add an explicit "do NOT return to caller after the grill" instruction in step 2 of the workflow. The grill-stall row was also added to the Quality gate failure modes table so the orchestrator has documented escalation when the rule fails to land.
