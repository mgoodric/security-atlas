# 05 — Parallel Batch Execution

Run N slices from a single orchestrator prompt while preserving per-slice rigor. Spawns one Engineer subagent per slice, each working in its own git worktree. **Use after slices 001 + 002 have merged** — that's when the dependency graph unlocks ≥ 10 parallel-safe streams.

## Prompt

```
Run a parallel batch of slices from docs/issues/. Use the per-slice template in Plans/prompts/04-per-slice-template.md verbatim for each.

Step 1 — Conflict-safe selection (report-back BEFORE acting):

1. Read docs/issues/_INDEX.md. Compute the "ready set": slices whose dependencies have all merged (check `git log --oneline main` for merged PR titles referencing issue numbers).
2. From the ready set, identify the largest conflict-free subset of size ≤ 3 (or N if I specify a different cap):
   - Two slices conflict if they would modify the same file. Predict file scope from each issue's narrative + cluster.
   - Spine-touching files (`go.work`, `package.json`, `pyproject.toml`, top-level `justfile`) are conflict hot-spots — at most ONE slice per batch may touch them.
   - Sequenced migration files (`migrations/NNNN_*.hcl`) need explicit sequence allocation if multiple slices add migrations.
3. Report-back format (WAIT for my approval before spawning anything):
   - Picked slices (issue # · title · cluster · estimate · expected files touched)
   - Why each picked slice is conflict-safe vs the others
   - Migration sequence numbers allocated, if any
   - Any ready-set slice you deliberately skipped and why
   - Cap recommendation (3 default; lower if review burden seems high)

Step 2 — On approval, set up worktrees:

For each picked slice, run from the main worktree:
  git worktree add ../security-atlas-<NNN> -b <cluster>/<NNN>-<slug> main

Step 3 — Spawn parallel Engineer subagents in ONE message:

Launch N Engineer subagents in parallel (single message, multiple Agent tool calls). Each agent's prompt is the per-slice template from Plans/prompts/04-per-slice-template.md, with these additions:
- Working directory: ../security-atlas-<NNN>
- Read CLAUDE.md from the worktree first
- Honor every workflow step (grill-with-docs → tdd → database-designer → security-review → simplify → ship-gate → changelog-generator)
- Open the PR from the worktree's branch
- Final report-back: PR URL · AC pass/fail table · files touched · CI run URL · time spent · surprises surfaced

Step 4 — Collect and summarize (after all N return):

- Table: slice # · PR URL · AC pass/fail · ship-gate status · merge-ready (Y/N)
- Conflicts detected between PRs (file overlap, migration collision, schema drift)
- Recommended merge order (deps + any post-hoc dependency surfaced during build)
- Suggested next batch (the new ready set after these merge)
- Update docs/issues/_INDEX.md Status column with PR URLs for the picked slices

Hard rules:
- N ≤ 3 unless I override
- Engineer subagent_type per slice — fresh context, no cross-contamination
- One subagent failing does NOT abort the others
- Quality gates apply per slice — do NOT relax any step "because it's a batch"
- If any picked slice surfaces an open question from Plans/canvas/11-open-questions.md, STOP that subagent; do not ship a guess

Use Algorithm mode in the orchestrator. Initialize a PRD (id: parallel-batch-<timestamp>).
```

## What to expect back

- A conflict-safe batch of N picked slices (N ≤ 3 by default)
- Report-back gate BEFORE any worktrees or PRs are created — you confirm or redirect
- N git worktrees at `../security-atlas-<NNN>/`, each on its own feature branch
- N parallel Engineer subagents, each running the per-slice template verbatim
- Summary at completion: PR URLs, AC pass/fail, ship-gate status, merge-ready flags
- `docs/issues/_INDEX.md` Status column updated with PR URLs for the picked slices

## How fidelity stays high

| Fidelity risk                             | Mitigation                                                                                     |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Context contamination between slices      | Each slice gets a fresh Engineer subagent — separate context window, separate `CLAUDE.md` read |
| Quality gates skipped under time pressure | Per-slice template runs verbatim; "do NOT relax any step" is explicit                          |
| Merge conflicts on shared files           | Conflict pre-check in Step 1; spine files capped at one slice per batch                        |
| Migration sequence collisions             | Explicit sequence allocation reported back in Step 1                                           |
| Review burden exceeds bandwidth           | N ≤ 3 cap; lower on any batch you don't trust                                                  |
| One slice's failure cascades              | Subagents independent; failure isolated to its PR                                              |

## Timing

| Mode                   | Wall-clock for 3 slices of estimates [1.5d, 2d, 1.5d] |
| ---------------------- | ----------------------------------------------------- |
| Serial (one at a time) | ~5 days                                               |
| Parallel batch (N=3)   | ~2 days (max of estimates, not sum)                   |

The orchestrator stays interactive during the wait — Engineer subagents run in the background.

## When to use which N

| N   | When                                                                    |
| --- | ----------------------------------------------------------------------- |
| 1   | After a hard slice, when you want a single point of focus               |
| 2   | When two slices are genuinely independent and you have review bandwidth |
| 3   | Default for sustained throughput after 001+002 merge                    |
| 4+  | Avoid unless you have collaborators reviewing PRs                       |

## Example: a concrete first batch

After slice 006 (SCF importer) merges, the natural conflict-safe trio is:

| Slice                                         | Cluster         | Why batchable                                     |
| --------------------------------------------- | --------------- | ------------------------------------------------- |
| **007** SOC 2 crosswalk loader                | Catalog         | Depends on 006 only; touches catalog/ tables      |
| **009** Control bundle format                 | Control-as-code | Independent of 007; touches control-bundle schema |
| **017** Scope dimensions + applicability_expr | Scope           | Independent; touches scope-related tables only    |

All three are on dependency Layer 2, no spine-file overlap.

## Notes

- The Step 1 conflict-check is the most important part of this prompt. Spine-touching files (`go.work`, top-level `package.json`, `justfile`) are the most common collision source — only one slice per batch may touch them.
- If two slices both need a migration, the orchestrator allocates explicit sequence numbers in the report-back. Don't let parallel agents pick their own — they'll collide.
- This prompt does NOT auto-merge PRs. Review each one, then merge in dependency order (the orchestrator's final summary recommends one).
- For the next batch after this one completes, just re-run the prompt — it'll compute the new ready set automatically.
- If a subagent's slice surfaces an architectural decision worth an ADR (`docs/adr/NNNN-title.md`), the subagent writes the ADR as part of its slice (per CLAUDE.md documentation discipline).
