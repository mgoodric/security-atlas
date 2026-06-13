# 741 — Auto-regenerate `_STATUS.md` and eliminate the per-merge reconcile PR

**Cluster:** Quality / Dev-process
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready` (no unmet dep — the generated-status system landed 2026-06-10 at `401b0b4b`)

## Narrative

The generated-status migration (`401b0b4b`, see `docs/issues/_GENERATOR.md`)
made `_STATUS.md` a pure function of ground truth: `scripts/gen-status.sh`
(`just status`) derives it from git history + open PRs + branches +
`_events.jsonl`. That was the right move — it erased a ~70 KB hand-maintained
log and the class of `chore(status)` reconcile churn that was two-thirds of
`main`'s commit history.

But it left one residual toil in place. `_STATUS.md` is still a **checked-in**
generated artifact, and after every slice merge its committed copy goes stale
(the just-merged slice now reads `merged` from git, but the committed table
still says `ready`). The parallel-batch / continuous-loop workflow
(`Plans/prompts/05-parallel-batch.md`, `Plans/prompts/07-continuous-batch-loop.md`)
therefore opens a **second PR per slice** — `chore(status): batch NNN -> merged`
— solely to re-commit the regenerated file. That is a full extra PR + CI cycle
per slice, recomputing something 100% derivable from `main`'s git log.

Crucially, this reconcile PR is **not gating anything**. The drift check
(`scripts/check-status-drift.sh`, CI job `Slice status · drift check`) is
explicitly **informational — "NOT in branch-protection.json, never blocks"** —
and it runs on **push-to-`main` only**, never on PR branches (a PR's own
not-yet-merged commit would false-flag). So the reconcile PR exists only to
keep the browsable in-repo table fresh and that one advisory job green. It
blocks no merge. It is pure cosmetic bookkeeping — exactly the toil the
generator migration set out to kill, surviving one step short of the goal.

This slice closes that gap: make `_STATUS.md` stay current **without a
human- or loop-authored PR**, and delete the reconcile step from the
workflows.

### Approach (the JUDGMENT call — recommended default + alternatives)

The implementer picks the mechanism and records the decision. The recommended
default is **A**; **B** and **C** are documented fallbacks if A's permission
model proves too thorny in this repo's branch-protection setup.

- **A (recommended) — auto-regenerate on push to `main`.** Replace the
  informational `status-drift` job with a job that, on push to `main`, runs
  `just status` and — only when the merged-set actually changed — commits the
  regenerated `_STATUS.md` back to `main` with `[skip ci]` and a bot identity
  (`github-actions[bot]`). The file stays in-tree and browsable; it is always
  fresh within one main-push; no slice ever needs a reconcile PR.
  **Load-bearing detail:** a bot push to `main` must satisfy branch protection.
  Resolve via the least-privilege option that works here — allow the
  Actions bot as a bypass actor for this path, or a narrowly-scoped deploy
  key / App token — and document the choice. If no acceptable bot-push path
  exists, fall back to B.

- **B — scheduled regeneration + drop the per-merge reconcile.** Keep the file
  but refresh it on a schedule (a daily cron `just status` commit, or fold the
  regeneration into the existing `docs-publish.yml` main-push pipeline, which
  already checks out full history). Accept that the in-repo file can be
  transiently stale between refreshes — it is a derived cache, and the drift
  job is already non-blocking, so transient staleness costs nothing
  operationally. The loop simply stops opening reconcile PRs.

- **C — drop the committed file entirely.** Remove `docs/issues/_STATUS.md`
  from the tree; make `just status` / `just status-preview` and the published
  docs site the only renderers. Update the ~10 in-repo references
  (`README.md`, `GOVERNANCE.md`, `CONTRIBUTING.md`, `OPERATIONS.local.md`,
  the prompts, etc.) to point at the generator command or a docs-site page.
  Most aggressive — eliminates the file, the job, and the reconcile forever —
  but loses the zero-tooling GitHub-browsable table, so it is the fallback of
  last resort unless the maintainer prefers it outright.

### Workflow + doc updates (in scope, all approaches)

This slice MUST also remove the now-obsolete reconcile step from the process
docs so the workflow and the mechanism stay in sync:

- `Plans/prompts/05-parallel-batch.md` — drop the final `just status` reconcile
  PR step (Step 6); the merge of the slice PR is the terminal action.
- `Plans/prompts/07-continuous-batch-loop.md` — drop the reconcile from the
  CONTINUATION contract; the loop schedules the next iteration directly after
  the slice PR merges.
- `Plans/prompts/06-status-reconcile.md` — already superseded by the generator;
  mark it fully retired (it is the manual flow the generator replaced).
- `docs/issues/_GENERATOR.md` — document the new "stays fresh automatically"
  behavior and that no reconcile PR is needed.
- `CLAUDE.md` — the "system of record for implementation is `main` plus the
  merge trail in `docs/issues/_STATUS.md`" line stays true; add a one-line
  note that `_STATUS.md` is auto-refreshed, not reconciled by hand.

Editing `05`/`07` here is deliberate and reviewed — it is the whole point of
the slice — and is distinct from the continuous loop's HARD RULE against
editing its own prompt mid-run. This is a filed, reviewed change to the
process, which is exactly what a slice is for.

## Threat model

STRIDE pass for a CI/automation + docs change. The dominant risk is a
self-triggering CI loop (a bot commit that re-triggers the job that made it)
and an over-privileged write token.

**S — Spoofing.** N/A — no auth surface changes.

**T — Tampering.** A bot that writes to `main` is a new write path. Mitigation:
the bot only ever writes the regenerated `_STATUS.md` (a deterministic function
of git history it just read), only when the merged-set changed, and the diff is
inspectable in the bot commit. It cannot write anything else. Approach B/C avoid
the write path entirely.

**R — Repudiation.** The bot commit is attributed to `github-actions[bot]` with
the workflow run id in the message — fully traceable.

**I — Information disclosure.** N/A — `_STATUS.md` is already public in-repo;
regenerating it exposes nothing new.

**D — Denial of service (primary for approach A).** A bot commit that re-fires
the workflow that produced it is an infinite CI loop. Mitigation (P0):
`[skip ci]` on the bot commit AND a no-op guard (commit only when `git diff`
on `_STATUS.md` is non-empty) — both, belt-and-suspenders. AC-3 asserts no
self-trigger.

**E — Elevation of privilege (primary for approach A).** A broad
`contents: write` token or an over-scoped bypass actor is the EoP risk.
Mitigation: least-privilege — scope the write to the single file / single
workflow path; prefer the Actions bot as a narrow bypass actor over a personal
PAT. Document the exact grant in the decisions log. AC-4 is the guard.

**Verdict:** has-mitigations — safe provided (A) the `[skip ci]` + no-op guard
prevent the CI loop (AC-3) and the write grant is least-privilege and
documented (AC-4); B and C carry neither risk and are the fallbacks if A's
permission model is unacceptable in this repo.

## Acceptance criteria

- [ ] **AC-1.** After a slice PR merges to `main`, `_STATUS.md`'s merged-set
      becomes current **without any human- or loop-authored reconcile PR** —
      verified by merging a representative change and observing the merged-set
      reflect it (approach A/B: automatically; approach C: the file is gone and
      `just status` renders current).
- [ ] **AC-2.** `Plans/prompts/05-parallel-batch.md` and
      `Plans/prompts/07-continuous-batch-loop.md` no longer instruct a
      `just status` reconcile PR; the slice-PR merge is the terminal action and
      the loop schedules the next iteration directly. `06-status-reconcile.md`
      is marked retired. `_GENERATOR.md` + `CLAUDE.md` updated.
- [ ] **AC-3 (P0, approach A only).** The auto-regenerate job cannot self-trigger:
      the bot commit carries `[skip ci]` AND the job is a no-op when the
      regenerated file is byte-identical to the committed one. Proven by the
      workflow logic + a dry-run showing a no-change push produces no commit.
- [ ] **AC-4 (P0, approach A only).** The write grant is least-privilege
      (scoped to the status path / single workflow, Actions-bot bypass
      preferred over a broad PAT) and the exact grant is recorded in the
      decisions log.
- [ ] **AC-5.** The informational drift signal is preserved or improved: a human
      can still tell at a glance whether the table is current (approach A/B: it
      is, by construction; the job, if kept, stays non-blocking — it must NOT
      become a merge gate). No new required/branch-protection context is added.
- [ ] **AC-6.** No in-repo reference to `_STATUS.md` is left dangling: every doc
      that links the file still resolves (approach A/B: unchanged path;
      approach C: every reference updated to the generator/docs-site target).
- [ ] **AC-7.** A decisions log at `docs/audit-log/741-status-auto-regen-decisions.md`
      records the chosen approach (A/B/C) and why, the permission model (if A),
      and the `detection_tier_actual` / `detection_tier_target` fields.

## Notes

Surfaced 2026-06-12 during the continuous-batch loop (maintainer asked why
`_STATUS.md` is still touched per-merge under the generated model). Parent
context: the generator migration `401b0b4b`. This is the last step of that
migration — the generator made status derivable; this makes it stay current
without the per-merge PR that derivation was supposed to retire.
