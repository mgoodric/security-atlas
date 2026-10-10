---
phase: specified
progress: 0
---

# Recovered OE work

Epic ISA. Inherits the Constraints in `isa/ISA.md` and cannot violate them.

## Problem

Two features were written by fires, committed, and never pushed. Their remote
branches were later deleted, so the commits survived only as local refs in
worktrees on one machine, whose Time Machine backup had been broken for eleven
days. Found 2026-08-29 while auditing what was still running on the
workstation.

Measured that day across this repository: 44 worktree branches, 25 absent from
every remote, and only 8 of those 25 clean and fully merged. The other 14 held
uncommitted changes or unpushed commits.

Both features build and pass their own package tests locally. Both fail this
repository's CI, each for one specific and named reason. That is the state
this epic closes.

## Vision

The two recovered features land on main having met the same bar as any other
contribution, with no floor lowered and no gate bypassed to get them there.

## Out of Scope

- Recovering the other twelve worktrees. Nine commits are tagged under
  `recovered/2026-08-29/*` and seven branches are pushed as `recover/*`;
  triaging the rest is separate work.
- The lifecycle fix that prevents recurrence. That lives in the engine's
  `preview-environments` epic, which added an anti-claim after this audit.
- Migrating `/v1/search` off its hardcoded query-parameter emission. Noted
  below because the merge surfaced it, but it is its own change.

## Goal

Both PRs merge green, and no coverage floor or generated-code gate was
weakened to achieve it.

## Claims

### R1 · sdk-go retry backoff (PR #1620)

- [ ] ISC-1: `pkg/sdk-go` meets its coverage floor with the recovered retry
      and backoff paths exercised. Falsifier: `coverage-gate` reporting
      `pkg/sdk-go` below `92.0%`. Measured 2026-08-29 at **84.2%**, which is
      what fails `Go · integration (Postgres RLS)` with `exit status 1`. The
      feature's own tests pass; what is missing is coverage of the new
      branches, not correctness of the existing ones.
- [ ] ISC-2 (guard): The floor is not lowered to make this pass. Falsifier:
      a change to the `pkg/sdk-go` entry in the coverage-gate configuration
      landing in the same change as the recovered feature.

### R2 · dashboard activity kinds (PR #1619)

- [ ] ISC-3: `internal/db/dbx/` matches what `sqlc generate` produces from the
      merged tree's SQL. Falsifier: `Go · sqlc generate diff` reporting
      `sqlc generate produced drift against committed internal/db/dbx/`.
- [ ] ISC-4: The activity feed's pagination cursor shape is decided
      deliberately, not adopted as a side effect of regeneration. Falsifier: a
      merged commit changing the cursor tuple with no decision recorded here.
      The drift shows the tuple moving from `(occurred_at, target_id, kind)`
      to `(occurred_at, kind, row_id)`, and ordering from
      `occurred_at DESC, kind ASC, target_id ASC` to
      `occurred_at DESC, kind ASC, row_id ASC`.
- [ ] ISC-5 (guard): Existing opaque cursors do not break silently. Falsifier:
      a base64 cursor issued before the change being accepted after it and
      returning a different page, rather than being rejected.

## Test Strategy

Every claim here closes on CI output rather than a local run, because local
runs are what allowed this state to exist. `go build`, `go vet`, `gofmt` and
the two package suites all pass on both branches today, and neither blocker
appears in any of them. The falsifiers name CI job output verbatim for that
reason.

## Anti-claims

- The system must never merge either branch by lowering a floor, bypassing a
  gate, or force-pushing past a failing check.
- The system must never regenerate `internal/db/dbx/` and treat a resulting
  API-visible change as mechanical. Regeneration resolves the drift; it does
  not decide whether the new cursor shape is acceptable.
- The system must never delete the `recovered/2026-08-29/*` tags or the
  `recover/*` branches until both PRs are merged or explicitly abandoned.

## Not yet specified

- Whether the cursor tuple change is acceptable at all, or whether the SQL
  should be changed back to preserve `(occurred_at, target_id, kind)`.
- Whether previously issued cursors need a compatibility window, and if so
  what rejects them cleanly.
- Whether `/v1/search` migrates onto the declarative `Query` field that this
  branch introduces, retiring `writeSearchOperationDetails`.

## Decisions

- D1 · Recover rather than respecify. Both features were confirmed absent from
  main three ways: `git merge-base --is-ancestor` returns false, a
  commit-message search across main's history finds only unrelated matches,
  and a symbol search finds nothing in main's tree. Rewriting them would pay
  twice for work that already exists and passes its own tests.
- D2 · Two features were dropped instead, having already landed by other
  routes: personnel security (14 files on main) and the incident register with
  calendar events (22 files on main).
- D3 · The recovered work is preserved independently of these PRs, as nine
  annotated tags under `recovered/2026-08-29/*` and seven pushed `recover/*`
  branches. Neither is removed until this epic closes.

## Changelog

### 2026-08-29 · specified

Written after recovering seven branches from worktrees whose remote branches
had been deleted. The audit that found them also found a repository bug: eslint
was linting gitignored Playwright output, producing 164 errors that failed the
pre-push hook for every branch in the repo until someone deleted the directory
by hand. That is fixed on main. These two claims are what remains.
