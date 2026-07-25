# Slice 741 — auto-regenerate `_STATUS.md`, eliminate the reconcile PR · decisions log

**Type:** JUDGMENT · **Approach:** A (maintainer-chosen) · **Date:** 2026-06-12

## What shipped

The informational `status-drift` CI job (which only DETECTED that the committed
`docs/issues/_STATUS.md` had gone stale relative to `main`) is replaced by a
`status-autoregen` job — `Slice status · auto-regenerate` — in
`.github/workflows/ci.yml`. On every push to `main` it regenerates `_STATUS.md`
from ground truth and, only when the file actually changed, commits **just that
file** back to `main` as `github-actions[bot]` with a `[skip ci]` message. The
per-merge `chore(status): batch NNN -> merged` reconcile PR is removed from the
parallel-batch (`05`) and continuous-loop (`07`) workflows; `06` is retired.

## Decisions made

### D1 — Approach A (auto-regenerate on push to main), as directed.

The maintainer pre-chose A. A keeps the zero-tooling GitHub-browsable in-tree
table (B's scheduled refresh leaves it transiently stale; C deletes the file and
breaks ~63 in-repo references). A is the minimal change that makes the file stay
current with no PR. **Confidence: HIGH** (directed + clearly the right trade-off
for a browsable derived cache).

### D2 — Invoke `scripts/gen-status.sh` directly, not `just status`.

The job runs `bash scripts/gen-status.sh` rather than `just status` so it has no
dependency on `just` being installed on the runner. `gen-status.sh` defaults its
output to `docs/issues/_STATUS.md` in place, so this is byte-identical to what
`just status` produces. **Confidence: HIGH.**

### D3 — Both self-trigger guards, belt-and-suspenders (AC-3, P0).

1. **No-op guard:** `git diff --quiet -- docs/issues/_STATUS.md` short-circuits
   with `exit 0` and makes **no commit** when the regenerated file is identical to
   the committed one. The common push (docs-only, or a push with no new slice
   merge) takes this path — so there is usually nothing to re-trigger on at all.
2. **`[skip ci]`** in the bot commit message: even when a commit IS made, GitHub
   Actions does not run `push`-triggered workflows for a head commit whose message
   contains `[skip ci]`. The bot push therefore cannot re-fire `status-autoregen`.

Either guard alone prevents the infinite-CI-loop (threat-model D); both are
present because the cost is a single `if` plus a commit-message token and the
failure mode (a runaway CI loop committing to main) is severe.
**Confidence: HIGH** (proven by the local dry-run below).

### D4 — Permission model: job-scoped `permissions: contents: write`, default `GITHUB_TOKEN` (AC-4, P0).

The exact grant:

```yaml
status-autoregen:
  permissions:
    contents: write # the ONLY scope; minimum to push one file to main
```

The workflow-level default stays `contents: read` (set at the top of `ci.yml`);
this single job widens exactly one scope. No `issues`, `pull-requests`, `actions`,
`packages`, or `id-token` scope is granted. The push uses the default per-run
`secrets.GITHUB_TOKEN` (the checkout token and the push token), **not** a personal
access token or an App token — the least-privilege, no-extra-secret option.

**Why no PAT / App / deploy key:** the merged `flake-counter.yml` (slice 352)
already commits a derived artifact to `main` with exactly this model and needs no
extra credential. `.github/branch-protection.json` records (≈ line 84): "main does
NOT block `GITHUB_TOKEN` pushes by default — the rule is 'require PR for human
pushes', but workflow-token pushes from a configured workflow are first-class."
`required_pull_request_reviews` is `null`, so there is no PR-review requirement for
the token to violate, and `required_signatures` is `false`, so the bot commit needs
no DCO/GPG signature. A PAT/App would be strictly more privilege for no benefit.
**Confidence: HIGH.**

### D5 — Fail-soft push, never red-X main before the bypass exists.

The push is `git push origin main` with a retry-once-on-conflict rebase, and a
final `else` branch that prints an actionable `::warning` (with the exact bypass to
configure) and `exit 0`. Rationale: the empirical default is that the push
succeeds (D4), but if a future ruleset tightening or `enforce_admins: true`
interaction ever rejects it, the job must not turn every main push red — staleness
of a derived cache is cosmetic (nothing is gated on `_STATUS.md`; the old drift
check was explicitly non-blocking and out of branch protection). **Confidence:
HIGH** for the posture; **MEDIUM** for whether the bypass is ever actually needed
(see D6 + Maintainer activation).

### D6 — Do NOT add a new required/branch-protection context (AC-5).

`status-autoregen` runs push-to-main-only and is deliberately absent from
`.github/branch-protection.json`'s `required_status_checks.contexts`. It must never
become a PR merge gate — it is the successor to the _informational_ drift check, and
the informational/non-blocking posture is preserved (AC-5). The "drift signal" is
now structurally satisfied: the file is regenerated on every push, so it cannot
drift. **Confidence: HIGH.**

### D7 — `_STATUS.md` kept at its canonical path (AC-6).

The file is regenerated in place at `docs/issues/_STATUS.md`; it is not moved or
renamed. The ~63 in-repo references (README, GOVERNANCE, CONTRIBUTING, the prompts,
CLAUDE.md, etc.) stay valid unchanged. **Confidence: HIGH** (verified: no rename in
the diff).

## Maintainer activation (the one-time step that _may_ be needed)

**Most likely: NOTHING to do.** Per D4, the `GITHUB_TOKEN` push to `main` is
expected to succeed with the current `branch-protection.json` (the `flake-counter`
precedent proves this). On the first push to `main` after this slice merges, watch
the `Slice status · auto-regenerate` job: if it prints `pushed regenerated
_STATUS.md to main`, **activation is complete and no config change is needed.**

**Only if the job logs the `push rejected` warning**, grant `github-actions[bot]` a
bypass for the push. The repo uses **classic branch protection** (the
`branches/main/protection` API, `enforce_admins: true`). Classic branch protection
does not have a per-actor bypass list, so use ONE of:

### Option A — migrate the `main` protection to a Repository Ruleset with a bypass actor (recommended)

Repo rulesets (the modern replacement for classic branch protection) support a
per-actor bypass list, which is exactly the least-privilege primitive needed.

1. GitHub UI: **Settings → Rules → Rulesets → New branch ruleset** (or convert the
   existing `main` protection). Target `main`. Re-add the same required status checks
   currently in `.github/branch-protection.json` so nothing is relaxed for humans.
2. In the ruleset's **Bypass list**, add the actor **`github-actions[bot]`**
   (Bypass mode: **Always**, or restrict to "for pull requests" is NOT what we want
   — we need direct-push bypass). This grants ONLY the Actions bot a direct-push
   bypass; human pushes still require the PR + checks.
3. Equivalent API call (the bypass actor is `actor_type: "Integration"`,
   `actor_id: 15368` = the GitHub Actions app):
   ```
   gh api -X POST repos/mgoodric/security-atlas/rulesets \
     --input ruleset.json
   # where ruleset.json includes:
   #   "bypass_actors": [
   #     { "actor_id": 15368, "actor_type": "Integration", "bypass_mode": "always" }
   #   ]
   ```
   (`actor_id: 15368` is the well-known GitHub Actions integration id; confirm with
   `gh api repos/mgoodric/security-atlas/rulesets/<id>` after creation.)
4. Keep `.github/branch-protection.json` (or a new `main-ruleset.json`) as the
   in-repo source of truth and note the bypass actor in its `$comment`.

### Option B — stay on classic branch protection, exempt `_STATUS.md` via a path

Classic branch protection cannot bypass a single actor for direct push, so if you
stay on classic protection the cleanest fallback is to keep the fail-soft behavior
(the job exits 0 and the file is refreshed on the _next_ successful push, or by a
local `just status` commit on a normal PR). This is the do-nothing path: the warning
is informational and the cache self-heals. No config change; accept transient
staleness identical to approach B's posture.

**Recommendation:** Option A (ruleset bypass actor) if/when the warning ever fires;
otherwise do nothing — the precedent says the push just works.

## Detection-tier classification (slice 353 / Q-13)

- `detection_tier_actual`: **none** — no bug surfaced during the slice. The change is
  a CI-job swap plus doc edits; the self-trigger and no-op behavior were verified by a
  local dry-run of the job's exact shell logic before opening the PR (see the PR body
  / Verification), not discovered as a defect.
- `detection_tier_target`: **integration** — were a regression to exist in this job
  (e.g. a missing `[skip ci]` causing a CI loop, or a non-`_STATUS.md` file being
  committed), the catch-point is the live push-to-main run after merge (the only tier
  that exercises the real `GITHUB_TOKEN` push to `main`; a PR run never triggers the
  push-to-main-only job). The local dry-run is the closest pre-merge proxy and is the
  primary mitigation; the first real post-merge push to main is the confirmation.

## Revisit once in use

- After the first few post-merge main pushes, confirm the job logs `pushed
regenerated _STATUS.md to main` and that no `[skip ci]` bot commit re-triggered CI.
  If the `push rejected` warning fires, perform Maintainer activation Option A.
- If the repo migrates `main` from classic branch protection to a ruleset for other
  reasons, fold the `github-actions[bot]` bypass actor into that migration.
