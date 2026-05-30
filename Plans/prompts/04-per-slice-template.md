# 04 — Per-Slice Template (issues 002 onward)

The canonical prompt shape for every slice **after** 001. Replace the **bolded** parts; don't change the workflow steps.

## Prompt template

```
Build docs/issues/<NNN>-<slug>.md.

HARD RULE — DO NOT STALL. The slice is not done until the PR is open on GitHub. If you find yourself drafting a "final report" or returning grill-with-docs output as a deliverable instead of executing the implementation, STOP drafting and finish the workflow. Commit + push + open PR + flip docs/issues/_STATUS.md row <NNN> from `in-progress` to `in-review` are non-optional steps, not appendices. The grill is a gate (pass through, do not return), not a deliverable. If the grill — or any later step — surfaces a subjective design question, resolve it YOURSELF with best-reasoned, pattern-matched judgment, record it in the decisions log (step 8b), and keep going. Only return to the caller for a true constitutional-invariant conflict — never for a routine design call.

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
8b. If the slice is `Type: JUDGMENT` — write the decisions log at `docs/audit-log/<NNN>-<slug>-decisions.md` (Decisions made · Revisit once in use · Confidence per decision — see "Slice types" below). Commit it with the slice. It does NOT block merge.
9. Status flip — append a `chore(status): <NNN> -> in-review` commit to the slice branch flipping `_STATUS.md` row <NNN>, push to the PR branch
9a. **Run `pre-commit run --all-files` one more time after the status-flip commit.** If prettier auto-fixes anything, amend the status-flip commit (`git commit --amend --no-edit -s`) before push. This catches the "prettier re-pads `_STATUS.md` after step 9" pattern that has caused 5+ CI fixups in recent batches. Slice 081 installs a pre-push hook that runs this automatically on engineer-installed setups (`just install-hooks`); this manual step is the belt-and-suspenders safety net for sessions running without the hook (fresh worktree, agent-driven push, etc.).

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

## Slice types: AFK and JUDGMENT

Every slice carries a `**Type:**` in its frontmatter. There are two values:

| Type       | Meaning                                                                                                                                                                                                                                             |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AFK`      | No subjective judgment calls. The ACs are mechanically verifiable. The slice ships when CI is green.                                                                                                                                                |
| `JUDGMENT` | The slice contains subjective calls — control-text accuracy, role-permission matrices, UX copy, rule-DSL expressiveness, OSCAL-tooling compatibility, docs authorship. **Claude makes the call**, records it, and the slice ships when CI is green. |

**`JUDGMENT` replaces the former `HITL` type.** The earlier model blocked a slice's merge on a human sign-off. In practice the human reviewer can't evaluate most of these calls with real confidence until the product is in actual use — so the sign-off was friction without signal. The new model: **Claude makes the judgment call using best-reasoned, pattern-matched judgment, writes it down, and the slice merges.** The human iterates post-deployment from the recorded revisit list.

**This changes the development process only.** The platform's **constitutional AI-assist boundary is untouched** — at runtime the _product_ still never publishes an audit-binding artifact without one-click human approval (CLAUDE.md "AI-assist boundary (hard)"). Dev-process judgment ≠ product runtime behavior. Never conflate the two.

### What a JUDGMENT slice does instead of a sign-off gate

A `JUDGMENT` slice writes a **decisions log** at `docs/audit-log/<NNN>-<slug>-decisions.md` (not a `-review.md` sign-off table). The decisions log has three sections plus a one-line detection-tier header:

1. **Decisions made** — each subjective call, the options considered, the chosen path, and the rationale (pattern-matched to existing slices / canvas / domain norms wherever possible).
2. **Revisit once in use** — an explicit, specific list of what the maintainer should re-evaluate once the product is running against real data / real auditors / real users. This is the iteration backlog. Be concrete: "re-check CC6.7's at-rest-only scoping once a TLS-config evidence_kind exists" beats "review the control kit."
3. **Confidence** — for each decision, a one-word confidence (`high` / `medium` / `low`). `low`-confidence decisions are the top of the revisit list.

**Detection-tier classification (slice 353 / Q-13).** Add two fields near the top of the decisions log capturing where any bug found DURING the slice was caught, and where it SHOULD have been caught:

```
- detection_tier_actual: <unit | integration | playwright | contract | manual_review | production | none>
- detection_tier_target: <unit | integration | playwright | contract | manual_review | production | none>
```

Use `none` for both when no bug surfaced during the slice (the common case for a clean docs/feature slice). When a bug IS caught — e.g. a sqlc-regen miss caught by CI integration, an RLS gap caught in review — record where it landed (`actual`) and the cheapest tier that should have caught it (`target`). Aggregated quarterly, a recurring `target=unit, actual=production` is a coverage-tier gap; `target=integration, actual=manual_review` (i.e. fix-forward) is an integration-enrolment gap (Q-7). The cost is one line; the payoff is the aggregate detection-tier signal the project lacks today (slice 333 Theme 3). See `CLAUDE.md` "Defect detection-tier classification".

The decisions log is committed as part of the slice. It does NOT block merge. It is the durable record that makes post-deployment iteration tractable.

(Historical `docs/audit-log/*-review.md` files from slices built under the old HITL model stay as-is — they're an accurate record of reviews that did happen. The new convention applies to slices built from here forward.)

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

| Gate                | Failure mode                                                                                | Action                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ------------------- | ------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `grill-with-docs`   | Canvas drift surfaced (e.g., the issue's AC contradicts an invariant)                       | STOP. Update the issue's AC or update the canvas — never paper over the drift                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `grill-with-docs`   | **Agent returns grill output (or a design question) as a "final report" without executing** | **Agent stall — orchestrator resumes with explicit "execute now" directive, OR closes out the work directly (per `Plans/prompts/05-parallel-batch.md` Failure-mode playbook).** Observed in slices 061, 028, 029, 042, 054. The grill is a gate, not a deliverable. A surfaced design question is NOT a reason to return — resolve it with pattern-matched judgment, log it in the decisions log, keep going. If an agent stalls twice, switch the approach (fresh agent with the design settled as facts) rather than resuming a third time. |
| `tdd`               | Test passes but mocks the DB or tests private method                                        | Reject the test. Re-write to hit a real DB through the public API                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `database-designer` | Migration is destructive or non-reversible                                                  | Re-design as additive; backfill in a separate slice                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `security-review`   | Secret in code, broad IAM, missing RLS context                                              | P0. Block merge until fixed                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `ship-gate`         | Critical findings present                                                                   | P0. Block merge until cleared                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |

## Notes

- The per-slice prompt is intentionally short. CLAUDE.md does the heavy lifting — quality gates, invariants, anti-patterns, style.
- Don't add scaffolding-approval clauses to per-slice prompts; that gate was passed by issue 001.
- If a slice surfaces a new architectural decision, write an ADR in `docs/adr/NNNN-title.md` as part of the slice (per CLAUDE.md documentation discipline).
- If an open question from `Plans/canvas/11-open-questions.md` blocks the slice, STOP and resolve the question first. Don't ship a guess. (Note: an _open question_ in that file is a real, logged, unresolved governance/architecture fork — distinct from a routine _design question_ a slice surfaces, which the agent resolves itself per the HARD RULE.)

## Why the HARD RULE preamble exists

The anti-stall callout at the top of the prompt was promoted from a buried orchestrator note (`Plans/prompts/05-parallel-batch.md`) after the same failure mode kept landing:

- **Slice 061 (batch 15):** engineer returned a 10-question grill analysis as a final report. Resumed once → completed cleanly.
- **Slice 028 (batch 16):** engineer returned an ADR draft + canvas reconciliation as a final report. Resumed once → completed cleanly.
- **Slice 029 (batch 18):** engineer announced "now invoking database-designer…" as its final transcript line and stopped. Resumed once → completed cleanly.
- **Slices 042 + 054 (batch 20):** both engineers returned a _genuine, well-pre-answered design question_ instead of self-resolving. 042 recovered on resume; 054 stalled a _second_ time, so the orchestrator switched approaches — a fresh agent with the design settled as facts, which executed straight through.

Lessons baked into this template: (1) the HARD RULE is the literal first line of the prompt body; (2) step 2 explicitly says "do NOT return to caller after the grill"; (3) a surfaced _design question_ is explicitly the agent's own to resolve (pattern-matched judgment + decisions-log entry) — not a reason to return; (4) the failure-modes table documents the two-strikes rule: after a second stall, switch the approach (fresh agent, settled spec) rather than resuming a third time.
