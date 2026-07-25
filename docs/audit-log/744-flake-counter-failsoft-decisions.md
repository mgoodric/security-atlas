# Slice 744 — fail-soft the flake-counter push + retire the dead status-autoregen job · decisions log

**Type:** JUDGMENT · **Approach:** path (c) accept-staleness (maintainer-chosen) · **Date:** 2026-06-13

- detection_tier_actual: production
- detection_tier_target: production

> The slice 741 `status-autoregen` push and the slice 352 `flake-counter.yml` push
> both fail only at runtime against the live protected branch (the rejection is a
> GitHub-side branch-protection decision, not a unit/integration-observable fault).
> The empirical signal (the `GH006` / "protected branch hook declined" / "11 of 11
> required status checks are expected" rejection) surfaced in the weekly
> flake-counter cron and in slice 741's first activation, i.e. in `production`. There is no cheaper
> local tier that could have caught it: the push only fails against the real remote
> ruleset. So `actual == target == production`; this is an infrastructure-config
> reality, not a coverage-tier gap.

## Context — surfaced by slice 741

Activating slice 741 (auto-regenerate `_STATUS.md`) revealed that its
`status-autoregen` job's `git push origin main` is rejected by branch protection,
and that the identical push in `.github/workflows/flake-counter.yml` (slice 352)
has been silently FAILING every week — a false weekly red X claiming the flake
budget is broken when nothing is wrong.

## Root cause (shared, verified 2026-06-12)

`main` branch protection requires status checks (strict) that a freshly-created
bot commit cannot satisfy, so a direct `git push origin main` is rejected for
**everyone**, including `github-actions[bot]` pushing with the default
`GITHUB_TOKEN`. On a **personal** (non-org) repo the clean per-actor ruleset
bypass is unavailable — the GitHub Actions integration cannot be added as a
ruleset bypass actor ("must be part of the ruleset source or owner
organization") — and disabling `enforce_admins` does not help (the token is not
admin-equivalent; tested empirically and the push still rejected). **No CI job on
this repo can currently push to `main`.**

## Decisions made

### D1 — Path (c) accept-staleness, as directed.

The maintainer pre-chose path (c). Of the three offered paths:

- **(a) open a PR instead of direct-push** — for `_STATUS.md` this resurrects the
  exact per-merge `chore(status)` reconcile PR that slice 741 set out to kill, a
  regression; it adds a bot-authored PR per change for a derived cache nobody
  reviews.
- **(b) provision a push credential with bypass** — a managed fine-grained PAT or
  GitHub App token plus an `enforce_admins: false` relaxation. This is real
  standing credential + a protection relaxation to maintain on a personal repo
  for the sole benefit of auto-refreshing two non-gating derived caches. The
  cost/benefit does not justify a long-lived bypass secret.
- **(c) accept staleness** — both files are non-gating (slice 741 removed the
  `status-drift` gate; the flake dashboard is informational). They are regenerated
  on demand (`just status`, `scripts/flake-counter.sh`) and are allowed to lag.
  git history + `_events.jsonl` remain the authoritative source. Staleness is
  cosmetic.

Path (c) is the minimal, no-new-credential, no-protection-change choice that
removes the false red X and removes the dead job, at the cost of an in-repo cache
that lags until a human refreshes it. **Confidence: HIGH** (directed + clearly the
right trade-off for two non-gating derived caches on a personal repo).

### D2 — Fail-soft ONLY the protected-branch rejection, never a real script error (P0 anti-criterion).

`flake-counter.yml`'s push step captures the push output and inspects it. If it
matches the rejection signature (`protected branch hook declined` / `GH006` /
`[remote rejected]`) the step prints an actionable message and `exit 0`. Any
**other** non-zero git result — or a failure of `scripts/flake-counter.sh` itself
upstream — propagates and still fails the run. The implementation:

- Captures `git push origin main 2>&1` into a variable.
- On a clean push: logs success.
- On a first-push protected-branch rejection: fail-soft, exit 0 (no rebase — a
  rebase cannot defeat branch protection).
- On a transient conflict (not a rejection): rebase once and re-push.
- On any residual failure: the shared `push_failsoft` helper re-checks the
  rejection signature — fail-soft only if it matches, otherwise emit a `::error`
  and `return 1` so the run fails.

This guarantees a genuine script error (e.g. the counter script crashing, a disk
error, an auth failure) is never swallowed. **Confidence: HIGH.**

### D3 — Remove the dead `status-autoregen` job from `ci.yml` (not fail-soft it).

Under path (c) the `status-autoregen` job serves no purpose: its only action is a
push that can never land, so it would always take the fail-soft branch and write
`_STATUS.md` to nowhere. Keeping it would be a permanently no-op job consuming a
runner on every push to main and implying a capability the repo lacks. It is
removed entirely (job + leading comment block).

Verified **not a required check**: the job ran on `push` to `main` only and is
absent from `.github/branch-protection.json`, so removing it drops no required
context and cannot block any PR. No other job's boundaries were touched (the
`changes` job above and `build-go` below are unchanged). **Confidence: HIGH.**

The flake-counter push is **fail-softed** rather than removed because that job has
a real purpose — it walks the CI history and files flake-investigation issues —
and only its final dashboard-commit step cannot land. Fail-softing that one step
preserves the useful work; removing the whole job would lose it. The asymmetry
(remove status-autoregen, fail-soft flake-counter) is deliberate.

### D4 — Correct the slice-741 auto-refresh over-claims in `_GENERATOR.md`.

`docs/issues/_GENERATOR.md` asserted CI auto-regenerates `_STATUS.md` on every
push and that `just status` is "rarely needed". That is false (the push is
blocked). The intro, daily-use note, the "Staying fresh" section, and the
migration-status wiring bullets are rewritten to the accurate accept-staleness
reality: the file is regenerated **on demand**, may **lag**, is **non-gating**,
and git history + `_events.jsonl` are ground truth. The useful "how a slice's
state is decided" content is kept verbatim. CLAUDE.md line 5 and
`branch-protection.json` were already corrected in PR #1368 and are not re-touched
here. **Confidence: HIGH.**

## Acceptance criteria

- **AC-1 (met).** `flake-counter.yml`'s push step exits 0 with an actionable
  message when the push is rejected by branch protection; no more weekly red X.
- **AC-2 (met).** Path (c) chosen + recorded here; applied to BOTH workflows —
  `flake-counter.yml` is fail-softed, `ci.yml`'s `status-autoregen` is removed
  (the path-(c) form of "applied consistently": neither auto-pushes).
- **AC-3 (met).** `_GENERATOR.md` auto-refresh claims corrected (D4). CLAUDE.md +
  `branch-protection.json` were corrected in #1368.
- **AC-4 (met).** `_GENERATOR.md`'s "Staying fresh" section documents the on-demand
  confirmation: run `just status`, then a clean `git status` means
  `docs/issues/_STATUS.md` matches ground truth. For the flake dashboard:
  `bash scripts/flake-counter.sh` locally regenerates `docs/flake-budget-dashboard.md`.

## Anti-criteria (held)

- Does NOT silently swallow a non-rejection error — D2's signature-gated guard
  fails the run on any non-rejection git failure.
- Does NOT weaken `main` protection for humans — no branch-protection change, no
  new credential, no bypass actor. Path (c) leaves the 11 required checks intact.
