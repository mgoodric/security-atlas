# 081 — Pre-push hook + post-status-flip pre-commit re-run guidance

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Surfaced during the 2026-05-15 post-batch-29 CI-failure investigation. The `pre-commit · all hooks` job failed **5 times today (8% of failures)** — second-most-common after the Playwright noise. The dominant pattern across those failures (and across the ~10 fixup commits I orchestrated during the v1 burndown):

**The "second commit after pre-commit ran" pattern:**

1. Engineer commits the slice work as commit #1. Runs `pre-commit run --all-files` — passes. Prettier may have auto-fixed files; engineer re-stages + commits #1 includes the prettier-fixed state.
2. Engineer creates commit #2: the status-flip commit (`chore(status): NNN -> in-review`) editing `_STATUS.md` to flip the row.
3. Engineer pushes both commits.
4. CI runs `pre-commit run --all-files` on the post-#2 state. Prettier notices `_STATUS.md`'s edit (different content from commit #1's verified state) wants to re-pad the table OR re-flow a long line. **Fails.** Engineer pushes a fixup commit, CI runs again (~1 min).

Specifically caught by this pattern today: slice 069's status-flip re-flow, slice 057's status-flip re-flow, slice 072's `route.test.ts` post-status-flip, slice 073's `_STATUS.md` post-status-flip. Each cost ~1 push + ~1 CI cycle + several minutes of orchestrator-or-engineer time.

The pattern's existence is documented (`feedback_local_ci_parity.md`, `Plans/prompts/04-per-slice-template.md` mentions running pre-commit before push). The pattern KEEPS HAPPENING because the slice-template step 9 says "append a status-flip commit and push" — it doesn't say "and run pre-commit AGAIN after that commit before push."

**Two fixes, both small, both AFK:**

### Fix A — `pre-push` git hook installed automatically

Add a `.husky/pre-push` (or equivalent — the repo's existing hook framework) that runs:

```
pre-commit run --from-ref=origin/main --to-ref=HEAD
```

…catching anything that pre-commit-run-against-staged would have caught but on the about-to-push state. This is mechanically what we want: an extra pre-commit gate at push time, not commit time. If pre-commit auto-fixes anything, the push fails (clearly) with instructions to commit the fix and retry.

The repo already has a pre-commit framework wired (per agentdiff hook output observed in every commit log: "ok git hooks pre-commit + post-commit + pre-push installed"). So `pre-push` slot already exists — this slice's job is to populate it with the right command.

### Fix B — slice-template step 9 update

Update `Plans/prompts/04-per-slice-template.md` step 9 from:

```
9. Status flip — append a `chore(status): <NNN> -> in-review` commit to the slice branch ...
```

…to:

```
9. Status flip — append a `chore(status): <NNN> -> in-review` commit to the slice branch ...
9a. **Run `pre-commit run --all-files` one more time after the status-flip commit.** If prettier
    auto-fixes anything, amend the status-flip commit (`git commit --amend --no-edit`) before push.
    This catches the "prettier re-pads _STATUS.md after step 9" pattern that's caused 5+ CI fixups
    in recent batches.
```

Fix A is the mechanical safety net. Fix B is the procedural reminder. Both ship in this slice. Together they reduce the ~5/day pre-commit-fixup load to ~0/day (subject to anything _new_ the engineer changes between pre-commit-run and push, which they shouldn't be doing).

**`npm run lint` integration (deferred, conditional on slice 078):**

The pre-push hook should ALSO run `npm run lint -w web` once that command works (currently broken per slice 078). The engineer's grill checks whether slice 078 has merged before the hook is committed:

- **If 078 merged**: hook runs `pre-commit run --all-files` AND `npm run lint -w web`. Catches both pre-commit AND eslint errors locally.
- **If 078 NOT merged**: hook runs `pre-commit run --all-files` only. `npm run lint -w web` is a TODO comment in the hook that the engineer SHOULD enable in their slice's commit once 078 lands. A follow-on slice file captures this dependency (status `not-ready`, dep on 078).

## Acceptance criteria

- [ ] AC-1: `.husky/pre-push` (or equivalent path per repo's existing hook framework — engineer verifies the path via the agentdiff output OR a manual `ls .git/hooks/` survey) populated with the `pre-commit run --from-ref=origin/main --to-ref=HEAD` invocation. Single shell-script file. ~5 lines.
- [ ] AC-2: `Plans/prompts/04-per-slice-template.md` step 9 updated per the narrative — adds a 9a sub-step calling out the "re-run pre-commit after status-flip commit" pattern. One-time edit to the canonical slice template.
- [ ] AC-3: `Plans/prompts/05-parallel-batch.md` "Failure-mode playbook" section gets a one-line addition: "Pre-commit failure on the status-flip commit specifically: run `pre-commit run --all-files` against the worktree, amend the status-flip commit, force-push. This is the dominant pre-commit failure shape and is fixed by slice 081's pre-push hook on engineer-installed setups; remote CI still catches it as a safety net."
- [ ] AC-4: `CONTRIBUTING.md` gains a "Local CI parity" subsection: explains the pre-push hook, how to bypass with `--no-verify` (warn against doing so casually + cite the recurring pre-commit-failure data from the 2026-05-15 post-mortem), and the additional `npm run lint -w web` integration after slice 078 lands.
- [ ] AC-5: Test the hook locally: commit a deliberate prettier-breakable change (e.g., a long line in a markdown file), attempt `git push`, confirm the hook blocks the push with a clear error message. Engineer runs the test before opening the PR; records the verification in the decisions log.
- [ ] AC-6: A `docs/audit-log/081-pre-push-hook-status-flip-guidance-decisions.md` records: (1) the exact hook-file path used (depends on whether the repo uses husky / lefthook / plain `.git/hooks/`), (2) the path to enable / re-enable `npm run lint -w web` after slice 078 lands, (3) whether AC-5's test caught the prettier-breakable change as expected, (4) any decisions about `--no-verify` policy.
- [ ] AC-7: If slice 078 has NOT merged at slice-run-time: file follow-on slice `docs/issues/<NNN>-pre-push-hook-add-lint.md` status `not-ready` with dep on 078. The follow-on enables the `npm run lint -w web` invocation in the hook (one-line uncomment). If slice 078 HAS merged: enable the lint invocation immediately, no follow-on slice needed.
- [ ] AC-8: Pre-commit clean. CI green on required checks. The hook is installed but does NOT block this slice's own push (engineer's working tree is clean enough to satisfy it).

## Constitutional invariants honored

- **Working norms — Surgical fixes**: smallest viable change. A 5-line hook script + a per-slice-template 9a sub-step + a CONTRIBUTING paragraph. No CI workflow changes, no production code changes.
- **AI-assist boundary**: nothing AI-generated. Hook script + doc updates.

## Canvas references

- _(none — local-dev infrastructure; canvas doesn't speak to git hooks)_

## Dependencies

- **078** (Unblock `npm run lint` after ESLint 10 + react-plugin incompat, ready) — _enabling, not blocking_. If 078 lands first, this slice can include the `npm run lint -w web` step in the hook; if not, the slice still ships without it + files a follow-on.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT make the hook un-bypassable. `git push --no-verify` MUST continue to work. The hook is local safety net; CI is the authoritative gate. Banning `--no-verify` would hurt legitimate cases (emergency hotfix, recovering from a corrupted hook environment).
- **P0-A2**: Does NOT install the hook system if one isn't already there. The repo already has a pre-push hook slot per the agentdiff output line; this slice populates it. If by some chance the slot doesn't exist, the engineer's grill records that and files a follow-on to add the hook framework BEFORE this slice's content lands.
- **P0-A3**: Does NOT add `npm run lint -w web` invocation if slice 078 hasn't merged (per AC-7). Adding it would break every engineer's push immediately. The engineer ALWAYS checks 078's merge status first.
- **P0-A4**: Does NOT add the hook script to any auto-execute path beyond `git push`. Specifically: does NOT add a `prepare-commit-msg` or `commit-msg` invocation that runs pre-commit on every commit (would add 1-3s × every commit, daunting on small dev-loop cycles).
- **P0-A5**: Does NOT update the slice-template + parallel-batch prompts in a way that changes the ORDER of steps or invalidates the existing slice template structure. It's an ADDITIVE 9a, not a refactor of step 9.

## Skill mix (3–5)

- Git hooks (pre-push specifically; framework-agnostic shell script)
- The repo's existing hook infrastructure (husky, lefthook, or plain `.git/hooks/`) — verify which is in use via the agentdiff output
- `engineering-advanced-skills:runbook-generator` (the CONTRIBUTING "Local CI parity" subsection IS a runbook)
- `simplify` (the hook script + the prompt-template addition stay tight — single-line additions where possible)

## Notes for the implementing agent

- **The hook content needs to be portable.** Don't hard-code a Python interpreter path; use `pre-commit` from `$PATH`. If pre-commit isn't installed, the hook should fail with a clear "install pre-commit first" message rather than silently passing.
- **Test the failure path before declaring success.** AC-5's test (introduce a deliberate prettier-breakable change, attempt push, confirm block) is non-negotiable. A pre-push hook that doesn't actually block on the failure modes it's supposed to is worse than no hook.
- **The CONTRIBUTING.md "Local CI parity" subsection is the engineer-facing docs.** Keep it tight — 3-5 lines: what the hook does, how to bypass (with a warning), where to file complaints if it's wrong. Anything longer reads like a manifesto and gets ignored.
- **Per-slice-template step 9a wording matters.** Engineers read the template literally; if 9a is ambiguous ("consider running pre-commit again"), they'll skip it. Use imperative ("Run `pre-commit run --all-files`"). The slice's quoted snippet in the narrative IS the recommended wording — verbatim is fine.
- **Beyond 5/day, the next-most-common pre-commit failure is `_STATUS.md` re-pad after merge-queue rebases** — slightly different shape from the status-flip pattern but same fix (pre-push catches it). Mention in the decisions log.
