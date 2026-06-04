# 457 — pre-commit guard against committing machine-local analysis caches

**Cluster:** Infra
**Estimate:** S
**Type:** JUDGMENT

**Status:** `ready` (no dependencies)

> Surfaced during slice 433.

## Narrative

Slice 433 added `.understand-anything/` to `.gitignore` and untracked the
cache. While doing so it surfaced that the ~9.8M cache had **already been
committed** to `main` in commit `a058b09a` ("chore(status): batch 164 -> merged
..."), swept in alongside a legitimate status reconcile — the exact `git add .`
footgun slice 433's threat model predicted, fired before the ignore rule landed.

`.gitignore` stops the _forward_ accident only for paths that are not yet
tracked. It does not catch the _next_ class of this accident: a different
machine-local cache (a new analysis tool, a fresh `.cache`-style dir, an
editor's project DB) swept in by a broad `git add` before anyone thinks to add
an ignore rule. The durable fix is a guard at commit time, not a per-tool
ignore rule chased after the fact.

## Scope

Two parts; the second is a decision, not necessarily code.

1. **Pre-commit / CI guard.** Add a guard (a `pre-commit` local hook and/or a CI
   step) that **fails when a staged path matches a known machine-local-cache
   shape** — minimally `.understand-anything/`, extensible to a small denylist
   of per-machine cache directory names. The guard fires on _staging for
   commit_, so the `a058b09a`-class accident is blocked at the source rather
   than discovered slices later. Must be a hard fail with an actionable message
   ("this looks like a machine-local cache; it belongs in `.gitignore`, not the
   tree"), not advisory.

2. **History-scrub decision (JUDGMENT).** Decide whether to scrub the existing
   9.8M `.understand-anything/` blob from `main`'s history (`git filter-repo` /
   BFG) before the repo goes public, weighed against the cost: rewriting `main`,
   a coordinated force-push, and breaking every outstanding clone/worktree.
   Record the decision (scrub-now vs. leave-inert-until-public-launch) in an ADR
   or the decisions log; do not perform a history rewrite without explicit
   maintainer sign-off (destructive op — CLAUDE.md "Ask before destructive
   operations").

## Acceptance criteria

- [ ] **AC-1.** Staging `.understand-anything/anything` for commit makes the
      guard fail with a clear, actionable message.
- [ ] **AC-2.** The guard runs in CI as well as locally (defense in depth —
      a contributor who skips local hooks is still caught).
- [ ] **AC-3.** The guard does NOT fire on legitimately-tracked paths (no false
      positive against the current tree — run it against `git ls-files`).
- [ ] **AC-4.** The history-scrub decision is recorded (ADR or decisions log)
      with the trade-off, regardless of which way it lands.

## Anti-criteria

- Does NOT perform a `git filter-repo` / BFG history rewrite without explicit
  maintainer sign-off (P0 — destructive).
- Does NOT re-add or modify the `.understand-anything/` ignore rule landed by
  slice 433.

## Dependencies

- Slice 433 (the ignore rule + untrack) — merged first.

## Notes

The existing pre-commit config already runs `detect-private-key` /
`detect-aws-credentials`; a `forbidden-paths`-style local hook is the natural
home for this guard (CLAUDE.md tech stack: pre-commit is already wired).
