# 744 — flake-counter weekly job: fail-soft the main push (and resolve the shared bot-push blocker)

**Cluster:** CI / Dev-process
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`

## Parent / surfaced-by

Surfaced 2026-06-12 while activating slice 741 (auto-regenerate `_STATUS.md`).
Investigating why 741's `status-autoregen` push to `main` was rejected revealed
that **`.github/workflows/flake-counter.yml` (slice 352) has the identical
`git push origin main` and has been silently FAILING every week** — its last two
runs (2026-06-01, 2026-06-08) both failed at the `Commit dashboard if changed`
step with:

```
remote: error: GH006: Protected branch update failed for refs/heads/main.
remote: - 11 of 11 required status checks are expected.
 ! [remote rejected] main -> main (protected branch hook declined)
```

## Root cause (shared with slice 741)

`main` branch protection has `enforce_admins: true` + 11 required status checks
(strict). A freshly-created bot commit cannot have passing required checks, so a
direct `git push origin main` is rejected for **everyone**, including
`github-actions[bot]` pushing with the default `GITHUB_TOKEN`. On a **personal**
(non-org) repo the clean per-actor ruleset bypass is unavailable (the GitHub
Actions Integration cannot be added as a ruleset bypass actor — "must be part of
the ruleset source or owner organization"), and disabling `enforce_admins` does
NOT help (the token is not admin-equivalent — verified empirically 2026-06-12).
So **no CI job on this repo can currently push to `main`.**

## Scope

Two deliverables:

1. **Fail-soft the flake-counter push (the S fix).** Mirror slice 741's
   `status-autoregen` transition-safety: if `git push origin main` is rejected,
   print the exact one-time activation step and `exit 0` instead of failing the
   run. This stops the weekly red X (a false "the flake budget is broken" signal)
   while the push path is unresolved. The dashboard simply does not update until
   the push path exists — acceptable, since the flake budget is informational.

2. **Resolve the shared bot-push blocker (the real fix — JUDGMENT decision).**
   Pick ONE path and apply it to BOTH flake-counter and 741's `status-autoregen`
   (they have the identical need):
   - **(a) Open a PR instead of direct-push.** The job uses `peter-evans/
create-pull-request` (or equivalent) with the default `GITHUB_TOKEN` —
     needs no branch-protection change (a PR goes through the required checks
     normally). Cost: a bot-authored PR per change (for `_STATUS.md`, this is
     back to the per-merge reconcile PR 741 set out to kill — so for 741 this is
     a regression; for the weekly flake dashboard it is fine).
   - **(b) Provision a push credential with bypass.** A fine-grained PAT or a
     GitHub App installation token owned by an admin, stored as a repo secret,
     used as the checkout/push token. Combined with `enforce_admins: false` (so
     the admin actor bypasses required checks) this lets the direct push land.
     Cost: a managed secret + a small admin-enforcement relaxation; this is the
     only path that keeps 741's "direct push, no PR" design.
   - **(c) Accept staleness.** Leave both jobs fail-soft; neither file
     auto-updates; regenerate on demand (`just status`, `scripts/flake-counter.sh`
     locally). Cost: the in-repo `_STATUS.md` / flake dashboard lag until a human
     refreshes them. Both are non-gating (741 removed the `status-drift` gate;
     the flake dashboard is informational), so staleness is harmless.

The deliverable-1 fail-soft is unconditional (do it regardless). Deliverable-2
is the maintainer's path choice; record it in the decisions log. Note the
**docs drift**: CLAUDE.md line 5 + `docs/issues/_GENERATOR.md` (updated by slice 741) currently assert `_STATUS.md` is "auto-refreshed by CI on every push to
main" — that is AspIRATIONAL until deliverable-2 lands; correct those lines to
match whichever path is chosen.

## Acceptance criteria

- [ ] **AC-1.** `flake-counter.yml`'s push step exits 0 with an actionable
      message when the push is rejected (no more weekly red X for the
      protected-branch rejection).
- [ ] **AC-2.** The shared bot-push path (a/b/c) is chosen, recorded in
      `docs/audit-log/744-flake-counter-failsoft-decisions.md`, and applied
      consistently to BOTH `flake-counter.yml` and `ci.yml`'s `status-autoregen`
      job.
- [ ] **AC-3.** The slice-741 docs that assert auto-refresh works (CLAUDE.md
      line 5, `_GENERATOR.md`) are corrected to match the chosen path (so the
      repo does not claim a capability it lacks).
- [ ] **AC-4.** A one-line check or note documents how to confirm the flake
      dashboard / `_STATUS.md` is current after the chosen path lands.

## Anti-criteria (P0)

- Does NOT silently swallow a NON-rejection error (a real script failure must
  still fail the run; only the `protected branch hook declined` push rejection
  is fail-softed).
- Does NOT weaken `main` protection for humans (if path (b), the admin-
  enforcement relaxation is scoped + documented; the 11 required checks stay).

## Notes

The empirical evidence (flake-counter's `GH006: 11 of 11 required status checks
are expected` failures + the rejected `status-autoregen` push + the personal-repo
ruleset limitation + the `enforce_admins: false` test that did not unblock the
bot) is captured in `~/.claude/MEMORY/STATE/continuous-batch-escalation.md` and
the slice 741 decisions log. The cheap win is AC-1 (stop the weekly red X); the
real decision is AC-2.
