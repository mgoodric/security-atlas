# 2883 — Migration version collisions: decisions log

Issue: mgoodric/security-atlas#2883. Extends `migration-timestamps-check`
(#1880).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The dispatcher caught the
  #2880 / #2874 collision on `20261283000000`, and three near-collisions
  (…275, …278, …280), by reading the numbers.
- **detection_tier_target:** contract. A CI check comparing the PR's new
  migrations against main.

## Decisions

### D1 — the duplicate-prefix rule was already right; the run was stale

On #2880's colliding tree (`89e91fffe5`), the existing script exits 1 and
names both files. That merge commit was pushed together with its renumber
fix, so it never got a CI run of its own. The PR's green came from
`6a875dd665`, which ran before #2874 landed on main. The gap is when the
check runs, not what it detects. So the duplicate rule is left as it is.

### D2 — compare against main fetched at check time, not the event's base

The job runs `git fetch --depth=1 origin main` and passes `FETCH_HEAD` as
`MIGRATIONS_BASE_REF`. `pull_request.base.sha` would give the same stale
view the merge ref already has. A migration the base lacks must carry a
timestamp strictly after the base's latest. This catches a version below
main's latest on an unused prefix, which the duplicate rule cannot see.
Renaming an existing migration also counts as new and fails, which is
intended: a renamed migration re-applies on existing installs.

### D3 — the merge-time re-check is the dispatcher's merge gate

Main has no merge queue, and "require branches to be up to date" is off. So
no CI run is guaranteed between main moving and a PR merging. Matt chose
(2026-11-21) not to change branch protection. The merge-time half of the
ticket's second criterion is covered by the dispatcher's merge gate. Right
before merging, it re-checks a PR's new migration numbers against main's
latest. If a merge queue is enabled later, this job already runs on
`merge_group` (it has no `if:`) and covers the same point in CI.
