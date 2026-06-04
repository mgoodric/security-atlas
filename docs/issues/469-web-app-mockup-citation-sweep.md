# 469 — Sweep `web/app/**` stale `Plans/mockups/` citations to the archived path

**Cluster:** Infra
**Estimate:** XS (< 0.25d)
**Type:** JUDGMENT

**Status:** `ready`

> Surfaced during slice 459 (mockup-comment provenance sweep). Slice 459 swept
> every LIVE `Plans/mockups/` citation it could reach (`internal/board`,
> `internal/catalog`, `web/components`, `web/lib`, `web/e2e`), but DEFERRED the
> ~23 hits under `web/app/**` to avoid a same-batch file collision with the
> in-flight slice 448 (which owned `web/app/`). With 448 now merged, that
> surface is free. Parent: slice 459 (grandparent: slice 437).

## Narrative

Slice 437 archived `Plans/mockups/` → `Plans/_archive/mockups/`. Slice 459
updated the stale provenance comments in every reachable surface, but the
`web/app/**` page/component files (~23 files, per slice 459's PR body and
`docs/audit-log/459-mockup-comment-sweep-decisions.md` revisit-note R1) still
carry `// per Plans/mockups/<page>.html`-style citation comments pointing at
the pre-archive path. They were left untouched only because slice 448 was
concurrently editing `web/app/` and a same-batch sweep would have collided.

This is the residual tail of the 459 sweep — comment-only, no behavior change.

## Scope

- **In scope:** update the stale `Plans/mockups/` provenance comments in
  `web/app/**` to `Plans/_archive/mockups/` (or rephrase to "the archived
  mockup"), matching the slice-459 convention exactly.
- **Out of scope:** any non-comment change; any file outside `web/app/**`
  (slice 459 already covered the rest); rewriting historical/dated records
  (reconcile logs, audit-log entries) that correctly cite the old path as
  point-in-time history.

## Acceptance criteria

- [ ] **AC-1.** Every LIVE `Plans/mockups/` provenance citation under
      `web/app/**` is updated to `Plans/_archive/mockups/` (verify with
      `grep -rn "Plans/mockups/" web/app | grep -v _archive` returning zero
      live-citation hits afterward).
- [ ] **AC-2.** Comment-only — no behavior, no test-assertion, no runtime
      path-resolver change. `web/` build + lint + tsc unaffected.
- [ ] **AC-3.** Any deliberately-invalid negative-test fixture string (cf.
      slice 459's `web/e2e-audit/lib/manifest.test.ts:47` precedent) is left
      as-is — do not rewrite a fixture that asserts the OLD path is rejected.
- [ ] **AC-4.** `pre-commit run --all-files` green.

## Threat model (STRIDE)

Comment-only documentation hygiene; no runtime, auth, or data surface. STRIDE
pass is trivially clean (same posture as parent slice 459).

## Notes

- Source list: slice 459's PR body + `docs/audit-log/459-mockup-comment-sweep-decisions.md`
  R1 (the ~23 flagged `web/app/**` files).
- No unmerged dependency now that 448 has merged → `ready`.
