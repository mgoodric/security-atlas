# 398 — Fix pre-existing `tsc --noEmit` errors in three web test files

**Cluster:** Web
**Estimate:** 1h
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 395, captured per continuous-batch policy.

While verifying slice 395 (a pure `@/lib/api` import-path codemod) with
`tsc --noEmit`, the project's standalone typecheck (`npm run typecheck`
= `tsc --noEmit`) was found to be **already red on `main`** with 15
errors across three test files — none touched by slice 395, and the
identical 15 errors reproduce on a clean checkout with slice 395's
changes stashed. Slice 395 introduces **zero** new errors (verified by
diffing the baseline vs. post-change error sets — identical).

The errors do not gate CI today because the web CI job runs `npm run
build` (`next build`), not standalone `tsc --noEmit`. `next build`
type-checks the production graph but does not compile these test files
into the bundle, so the strict-mode nits in them never surface in CI.
`vitest` runs the files fine at runtime (all 1204 tests green). The gap
is only in the standalone `typecheck` script, which is not wired into
CI.

### The 15 errors

1. `web/lib/auth/oauth-client.test.ts` (3 errors): two unused
   `@ts-expect-error` directives (TS2578) and a `window.location` mock
   missing `Location` members (TS2740).
2. `web/next-config.test.ts` (1 error): indexing a `Rewrite[] | {...}`
   union by `[0]` without narrowing (TS7053).
3. `web/scripts/capture-readme-screenshots.test.ts` (11 errors): passing
   partial env objects to a `ProcessEnv` param missing `NODE_ENV`
   (TS2345).

### What ships

1. Fix each of the 15 errors in place (narrow the union; complete the
   `Location` mock or cast; add `NODE_ENV` to the test env objects or
   widen the helper's param type; drop the unused `@ts-expect-error`s).
2. Optionally wire `npm run typecheck` into CI as a required check so
   `tsc --noEmit` cannot regress again silently (JUDGMENT — maintainer's
   call; a CI-workflow edit is out of slice 395's scope and not done
   here).

## Acceptance criteria

- [ ] **AC-1.** `cd web && npx tsc --noEmit` exits 0 (zero errors).
- [ ] **AC-2.** `npm run lint`, `npm run test`, and `next build` stay
      green. No behavior change — type-correctness only.

## Dependencies

- None (independent of slice 395; can land before or after).

## Anti-criteria (P0 — block merge)

- **P0-398-1.** Does NOT change runtime behavior — type/test-fixture
  correctness only.
- **P0-398-2.** Does NOT auto-merge.
