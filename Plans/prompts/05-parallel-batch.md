# 05 — Parallel Batch Execution

Run N slices from a single orchestrator prompt while preserving per-slice rigor. Spawns one Engineer subagent per slice, each working in its own git worktree. **Use after slices 001 + 002 have merged** — that's when the dependency graph unlocks ≥ 10 parallel-safe streams.

This prompt has been evolved across four batches (014/017/039, 013/019/024, 018/036/044, 009/045/046). The current form bakes in those lessons. See `~/.claude/projects/-Users-gmoney-Development-security-atlas/memory/feedback_parallel_batch_patterns.md` for the full lesson set.

## Prompt

```
Run a parallel batch of slices from docs/issues/. Use the per-slice template in Plans/prompts/04-per-slice-template.md verbatim for each.

Step 1 — Conflict-safe selection (report-back BEFORE acting):

1. Read docs/issues/_STATUS.md. The ready set is all rows with status `ready`. (Authoritative — no git-log archaeology needed; if _STATUS.md feels stale, run Plans/prompts/06-status-reconcile.md first.)
2. From the ready set, identify the largest conflict-free subset of size ≤ 3 (or N if I specify a different cap):
   - Two slices conflict if they would modify the same file. Predict file scope from each issue's narrative + cluster.
   - Spine-touching files (`go.work`, `package.json`, `pyproject.toml`, top-level `justfile`) are conflict hot-spots — at most ONE slice per batch may touch them.
   - Sequenced migration files (`migrations/NNNN_*.sql`) need explicit sequence allocation if multiple slices add migrations.
   - `internal/api/httpserver.go` is a known shared touch-point (every functional slice appends a route). Mount-line appends are now a known-safe 3-way merge per the canonical pattern.
   - `internal/api/schemaregistry/registry.go` (`DefaultSeed`) is a shared touch-point for any slice that adds an `evidence_kind` schema.
   - sqlc generated files (`internal/db/dbx/{models,querier}.go`) conflict on every multi-slice batch — resolve via `sqlc generate` post-rebase, never by hand.
3. Report-back format (WAIT for my approval before spawning anything):
   - Picked slices (issue # · title · cluster · estimate · expected files touched)
   - Why each picked slice is conflict-safe vs the others
   - Migration sequence numbers allocated, if any
   - Any ready-set slice you deliberately skipped and why
   - Cap recommendation (3 default; lower if review burden seems high)
   - Open-questions check: explicitly state that no picked slice has an unresolved open question in Plans/canvas/11-open-questions.md

Step 2 — On approval, set up worktrees AND flip status atomically:

For each picked slice, run from the main worktree:
  git worktree add ../security-atlas-<NNN> -b <cluster>/<NNN>-<slug> main

Then update docs/issues/_STATUS.md: flip each picked slice from `ready` to `in-progress`, fill `Branch` + `Started` columns. Commit directly to main: `chore(status): batch <NNN1, NNN2, NNN3> → in-progress`. This claim-stake prevents another batch (or solo session) from racing on the same slice.

Step 3 — Spawn parallel Engineer subagents in ONE message:

Launch N Engineer subagents in parallel (single message, multiple Agent tool calls). Each agent's prompt is the per-slice template from Plans/prompts/04-per-slice-template.md, with these additions:

- Working directory: ../security-atlas-<NNN>
- Read CLAUDE.md from the worktree first
- Honor every workflow step (grill-with-docs → tdd → database-designer → security-review → simplify → ship-gate → changelog-generator)
- Open the PR from the worktree's branch
- Final report-back: PR URL · AC pass/fail table · files touched · CI run URL · time spent · surprises surfaced

PLUS these explicit pattern-reuse directives (drawn from prior batches' learnings):

- HARD RULE — DO NOT STALL. The slice is not done until the PR is open on GitHub. If you find yourself drafting a "final report" after Skill("security-review") or Skill("ship-gate") and before the PR exists, stop drafting and finish the workflow first. Commit + push + open PR + flip _STATUS.md to `in-review` are non-optional steps, not appendices.
- Use the established `internal/api/httpserver.go` Mount-append pattern (slices 014/017/018/019/024/036/009 are reference). Never add a second `chi.NewRouter().Mount("/", ...)` — chi panics. Use `root.Get/Post/Patch/Delete` directly.
- Match slices 014/017/018/036's four-policy RLS pattern for any new tenant-scoped table (`tenant_read` FOR SELECT, `tenant_write` FOR INSERT WITH CHECK, `tenant_update` FOR UPDATE USING + WITH CHECK, `tenant_delete` FOR DELETE — all under `FORCE ROW LEVEL SECURITY`). For append-only audit tables (slices 013, 036) use only `tenant_read` + `tenant_write` — the explicit absence of update/delete policies under FORCE makes the table append-only by construction.
- If you ALTER an existing table to add a NOT NULL column (or a new CHECK constraint), ALSO patch slice 002's `internal/db/integration_test.go` test helpers (`mustInsertControl`, `mustInsertRisk`, etc.) to supply the new column with a sensible default value — same precedent as slices 019, 018, 009. Without this patch, slice 002's existing RLS tests break.
- Test-fixture tokens MUST be neutral test strings, NOT vendor token prefixes. Banned in test code AND comments: `okta_*`, `ops_*` + base64-shaped suffixes, `SSWS <key>`, `ghp_*`, `gho_*`, `xox[bp]-*`, `sk_live_*`, `sk_test_*`, `ya29.*`, `AKIA*`, `AIza*`, `eyJ*` (JWT prefix). GitGuardian flags these even in test files. Use `"test-token"`, `"test-token-redaction-check"`, `"test-apply-token"` etc.
- For sqlc-touching slices: ALWAYS regenerate `internal/db/dbx/*.go` via `sqlc generate` after editing `internal/db/queries/*.sql` or `sqlc.yaml`. Never hand-edit the generated files.
- For pgx-using code: avoid using the same `$N` placeholder in two incompatible type contexts within one statement (e.g. UUID and text-concat input) — pgx's prepare phase errors with SQLSTATE 42P08. Compute derived values in Go.
- `pre-commit run --all-files` locally before push. Prettier reformatting of CHANGELOG.md has been caught by CI four times across batches — this single step prevents it.

STATUS TRANSITION (this is Step 9 of the subagent's own per-slice workflow, after the PR is open):
- From the MAIN worktree (`/Users/gmoney/Development/security-atlas`), pull --rebase, then flip the picked slice's row in `docs/issues/_STATUS.md` from `in-progress` to `in-review`, fill the `PR` column with `gh#<N>`. Commit `chore(status): <NNN> -> in-review`. Push.
- If another agent has updated _STATUS.md in the meantime, re-apply just your one row.

Step 4 — Collect and summarize (after all N return):

- Table: slice # · PR URL · AC pass/fail · ship-gate status · merge-ready (Y/N)
- Conflicts detected between PRs (file overlap, migration collision, schema drift)
- Recommended merge order (deps + any post-hoc dependency surfaced during build)
- Suggested next batch (the new ready set after these merge)
- Update docs/issues/_STATUS.md: confirm each picked slice transitioned to `in-review`; fill `PR` column from each subagent's final report if not already populated. Commit on main: `chore(status): batch <NNN1, NNN2, NNN3> → in-review`.

Hard rules:
- N ≤ 3 unless I override
- Engineer subagent_type per slice — fresh context, no cross-contamination
- One subagent failing does NOT abort the others
- Quality gates apply per slice — do NOT relax any step "because it's a batch"
- If any picked slice surfaces an open question from Plans/canvas/11-open-questions.md, STOP that subagent; do not ship a guess

Subagent failure-mode playbook (orchestrator behavior):

- **Stall after security-review or ship-gate** (subagent returns its review block instead of opening the PR): resume the agent once with explicit "commit + push + open PR + flip status" instructions. If the second attempt also stalls, the orchestrator closes out the work directly: `git status` the worktree, write the CHANGELOG entry from the agent's drafted bullet, `pre-commit run --all-files`, stage by name, commit with Conventional Commit + Co-Authored-By, push, `gh pr create` from the worktree, then flip _STATUS.md in the main worktree. This is the dominant failure mode across batches 2-4 (slices 017, 036, 044, 045, 046).

- **CI fails on prettier reformat of CHANGELOG.md**: one-character fix in the worktree, push, watch CI. If recurring across multiple slices in the batch, update the subagent prompts on the next batch.

- **CI fails on GitGuardian Security Checks**: check if HEAD content is clean. If yes, the flag is historical (GitGuardian is branch-scoped, not HEAD-scoped). Squash-rebase the branch to one clean commit:
```

git rebase origin/main # resolve any conflicts
git reset --soft origin/main # drop commit history, keep working state
git commit -m "<single squashed message>"
git push --force-with-lease

```
GitGuardian re-scans against the single clean commit → flag clears.

- **CI fails on `Go · integration (Postgres RLS)` with NOT-NULL violation on slice-002 test helper INSERTs**: this slice's migration ALTERed a slice-002 table to add a NOT NULL column. Patch `internal/db/integration_test.go`'s `mustInsertControl`/`mustInsertRisk`/etc. helper to supply the new column with a sensible default. Same precedent: slices 009, 018, 019.

- **CI fails on sqlc-related issue (querier interface mismatch, models out of sync)**: run `sqlc generate` in the worktree, stage the regenerated files, commit `chore(sqlc): regenerate after rebase`, push.

- **Merge conflicts on rebase against new main**: resolve `sqlc.yaml` by hand (append-only schema list), run `sqlc generate` to resolve `dbx/{models,querier}.go`, resolve `CHANGELOG.md` by integrating both bullets into `[Unreleased] / Added`, resolve `internal/api/httpserver.go` by keeping both Mount/Get/Post route registrations.

Use Algorithm mode in the orchestrator. Initialize a PRD (id: parallel-batch-<timestamp>).
```

## What to expect back

- A conflict-safe batch of N picked slices (N ≤ 3 by default)
- Report-back gate BEFORE any worktrees or PRs are created — you confirm or redirect
- N git worktrees at `../security-atlas-<NNN>/`, each on its own feature branch
- A claim-stake commit on main marking the picks as `in-progress` (prevents racing)
- N parallel Engineer subagents, each running the per-slice template verbatim
- Summary at completion: PR URLs, AC pass/fail, ship-gate status, merge-ready flags
- `docs/issues/_STATUS.md` updated to `in-review` for each picked slice (per-slice transition commit + orchestrator confirmation commit)

## How fidelity stays high

| Fidelity risk                                    | Mitigation                                                                                                                                                             |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Context contamination between slices             | Each slice gets a fresh Engineer subagent — separate context window, separate `CLAUDE.md` read                                                                         |
| Quality gates skipped under time pressure        | Per-slice template runs verbatim; "do NOT relax any step" is explicit                                                                                                  |
| Merge conflicts on shared files                  | Conflict pre-check in Step 1; spine files capped at one slice per batch; known-safe patterns documented for `httpserver.go`, sqlc files, `CHANGELOG.md`, `DefaultSeed` |
| Migration sequence collisions                    | Explicit sequence allocation reported back in Step 1                                                                                                                   |
| Review burden exceeds bandwidth                  | N ≤ 3 cap; lower on any batch you don't trust                                                                                                                          |
| One slice's failure cascades                     | Subagents independent; failure isolated to its PR                                                                                                                      |
| Subagent stalls before opening PR                | HARD RULE preamble + orchestrator close-out playbook after 2 failed resumes                                                                                            |
| Slice ALTERs a slice-002 table → existing breaks | Subagent prompt explicitly directs the slice-002 test helper patch                                                                                                     |
| CI catches a regression the agent missed         | Failure-mode playbook covers the four recurring shapes (prettier, GitGuardian, sqlc, NOT NULL fixture)                                                                 |

## Timing

| Mode                   | Wall-clock for 3 slices of estimates [1.5d, 2d, 1.5d] |
| ---------------------- | ----------------------------------------------------- |
| Serial (one at a time) | ~5 days                                               |
| Parallel batch (N=3)   | ~2 days (max of estimates, not sum)                   |

The orchestrator stays interactive during the wait — Engineer subagents run in the background.

## When to use which N

| N   | When                                                                                                                                |
| --- | ----------------------------------------------------------------------------------------------------------------------------------- |
| 1   | After a hard slice, when you want a single point of focus, OR for solo-by-design slices (033 RLS-everywhere; 034 OIDC; 050 release) |
| 2   | When two slices are genuinely independent and you have review bandwidth                                                             |
| 3   | Default for sustained throughput after 001+002 merge                                                                                |
| 4+  | Avoid unless you have collaborators reviewing PRs                                                                                   |

## Solo-by-design slices (do NOT batch these)

These slices touch surfaces that conflict with every concurrent slice. Run them alone.

| Slice                                     | Why solo                                                                                           |
| ----------------------------------------- | -------------------------------------------------------------------------------------------------- |
| 033 Postgres RLS enforcement everywhere   | Touches RLS on every existing tenant-scoped table — races every slice that adds a table            |
| 034 OIDC RP + local users + api_keys CRUD | Substantive auth refactor (OIDC flow + sessions + web/ login + api_keys table + admin endpoints)   |
| 050 Public release readiness + automation | License / persona / sanitize judgment calls; HITL across many files                                |
| 007 SOC 2 v2017 (TSC) crosswalk loader    | HITL on 20 mapping spot-checks — needs focused review attention, not split between three reviewers |
| 022 Policy library + 5 stock policies     | HITL on stock policy text — same focused-review reason as 007                                      |

## Example batches (historical, for pattern reference)

- **Batch 1** (014, 017, 039) — schema registry + scope dimensions + release pipeline. Zero spine touches. Lessons surfaced: chi double-Mount panic, RLS WITH CHECK pattern.
- **Batch 2** (013, 019, 024) — evidence ledger + risk register + vendor lite. Slice 019 patched slice-002 risk test fixture for new CHECK. Slice 013 surfaced the test-isolation issue between schemaregistry + ingest packages.
- **Batch 3** (018, 036, 044) — framework-scope workflow + S3 + GitHub connector. Slice 036 burned three CI iterations on GHA service-container quirks (bitnami/minio unpullable; mc image entrypoint). Implements ADR-0001 (slice 018).
- **Batch 4** (009, 045, 046) — control bundle + Okta + 1Password connectors. Slice 009 surfaced pgx single-placeholder type-inference error. Slices 045 + 046 both hit GitGuardian on vendor token prefixes; resolved by squash-rebase.

## Notes

- The Step 1 conflict-check is the most important part of this prompt. Spine-touching files (`go.work`, top-level `package.json`, `justfile`) are the most common collision source — only one slice per batch may touch them.
- If two slices both need a migration, the orchestrator allocates explicit sequence numbers in the report-back. Don't let parallel agents pick their own — they'll collide.
- This prompt does NOT auto-merge PRs. Review each one, then merge in dependency order (the orchestrator's final summary recommends one).
- For the next batch after this one completes, just re-run the prompt — it'll compute the new ready set automatically from `_STATUS.md`.
- If a subagent's slice surfaces an architectural decision worth an ADR (`docs/adr/NNNN-title.md`), the subagent writes the ADR as part of its slice (per CLAUDE.md documentation discipline). ADR-0001 (FrameworkScope workflow) is the existing reference.
- The orchestrator should be prepared to close out subagent work directly when an agent stalls. See "Subagent failure-mode playbook" in the prompt.
