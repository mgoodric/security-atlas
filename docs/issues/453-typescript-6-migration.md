# 453 — TypeScript 6 migration

**Cluster:** Infra
**Estimate:** M (1-2d)
**Type:** AFK

**Status:** `ready`

## Narrative

`web/package.json` **declares** `typescript: "^6"` but the **installed** version
is `5.9.3` — the `^6` range has never resolved to an actual 6.x because the
lockfile predates a 6.x release. Dependabot PR **#635** wants
`typescript 5.9.3 → 6.0.3`, the first real 6.x. `tsc --strict` is a CI gate
(the `web` typecheck job + `npm run typecheck`), so a TS major is not a
no-op bump — TS 6 removes deprecated compiler flags and tightens `lib` type
definitions, either of which can newly fail a strict typecheck.

This slice performs the actual 6.x migration: install 6.x, fix the new
strictness / `lib`-type breakages across `web/`, and verify the surrounding
toolchain still composes — specifically `eslint-config-next@16` and
`@types/react@19`, which have their own TS-version peer expectations. It
**supersedes dependabot PR #635**.

**Scope discipline.** Compiler bump + minimal breakage fixes only. No
`tsconfig` strictness _additions_ beyond what TS 6 mandates, no refactor of
typed code beyond what the upgrade forces, no `@types/*` bumps except those
strictly required for TS-6 compat.

## Threat model

STRIDE pass. Runtime-security surface is minimal (TypeScript is a build-time
typechecker; it ships no runtime code). The notable upside: a stricter typecheck
can surface a **latent null/undefined-handling bug** — which is a _good_ outcome
to be fixed correctly, never suppressed.

**S / R / I / E**

- _Threat:_ None directly — `tsc` runs at build/CI time only; it touches no
  auth, RLS, tenant data, or release-signing surface.
- _Mitigation:_ Confirm the change is confined to `web/` dev-tooling +
  whatever `web/` source lines TS 6 forces to change.

**T — Tampering (reframed: latent-bug discovery)**

- _Threat / opportunity:_ TS 6 tightens `lib` types (e.g. stricter DOM/`Array`/
  `Object` signatures) and may newly flag an unsound cast or an unchecked
  null in `web/lib/api.ts`, `web/lib/api/bff.ts`, or a BFF route handler — code
  that sits on the request path between the browser and the Go platform.
- _Mitigation:_ Fix each new error by **correcting** the null/undefined handling
  (narrowing, guards, honest types), NOT by adding `as any` / `@ts-ignore` /
  `@ts-expect-error` or by loosening `tsconfig`. A new error that reveals a real
  latent bug is logged in the PR body as a found-defect.
- _Anti-criterion:_ P0-453-2.

**D — Denial of service**

- _Threat:_ A `tsc` config incompatibility could make the typecheck pass
  vacuously (e.g. a removed flag silently disabling a check), weakening the
  gate.
- _Mitigation:_ Confirm `tsc --strict --noEmit` still runs over the full `web/`
  source set (non-zero file count) and that no strictness flag was dropped to
  make it pass.
- _Anti-criterion:_ P0-453-3.

## Acceptance criteria

- [ ] **AC-1.** `web/package.json` `typescript` resolves to an installed `6.x`
      (lockfile updated; `npx tsc --version` reports 6.x).
- [ ] **AC-2.** `npm run typecheck -w web` (`tsc --noEmit`, strict) exits 0 with
      no new suppressions (no added `as any` / `@ts-ignore` /
      `@ts-expect-error`).
- [ ] **AC-3.** Any TS-6-introduced errors are fixed by correcting the types /
      null handling, not by loosening `tsconfig` or suppressing; each
      non-trivial fix noted in the PR body.
- [ ] **AC-4.** `eslint-config-next@16` + `@types/react@19` compatibility
      verified: `npm run lint -w web` exits 0 and the eslint TS parser does not
      crash on TS 6.
- [ ] **AC-5.** `npm run build -w web` (Next.js build, which runs `tsc`)
      succeeds.
- [ ] **AC-6.** The `Frontend · vitest` + `Frontend · Playwright e2e` CI jobs
      remain green (no behavior change from the type fixes).
- [ ] **AC-7.** If a latent null/undefined bug was surfaced and fixed, it is
      called out explicitly in the PR body (found-defect record).
- [ ] **AC-8.** `tsconfig` strictness is unchanged except where TS 6 _mandates_
      a change; no opportunistic loosening.
- [ ] **AC-9.** `pre-commit run --all-files` passes; CI green. PR body notes
      "Supersedes #635".

## Constitutional invariants honored

- **TS tooling — `tsc --strict` gate (CLAUDE.md tech stack).** The migration
  keeps the strict gate intact and does not weaken it to pass.

## Canvas references

- CLAUDE.md tech-stack table — "TS tooling — `tsc --strict`".
- `Plans/canvas/09-tech-stack.md` — frontend toolchain.

## Dependencies

- Supersedes **dependabot #635** (typescript 5.9.3 → 6.0.3).
- **#078** (eslint/next toolchain pin) — `merged`; relevant to the
  eslint-config-next compat check.

## Anti-criteria (P0 — block merge)

- **P0-453-1.** Does NOT bump `eslint-config-next`, `@types/react`,
  `@types/node`, or other deps beyond what TS-6 compat strictly requires
  (surgical compiler bump).
- **P0-453-2.** Does NOT suppress new TS-6 errors with `as any` / `@ts-ignore` /
  `@ts-expect-error` or by loosening `tsconfig` — fix the types.
- **P0-453-3.** Does NOT let the typecheck pass vacuously (a dropped strictness
  flag that disables a check is a regression, not a fix).
- **P0-453-4.** Does NOT refactor typed code beyond the minimum the upgrade
  forces.

## Skill mix (3-5)

- `dependency-auditor` — the TS 6 release notes / removed-flags list.
- `tdd` — re-run the vitest + Playwright suites as the no-behavior-change gate.
- `simplify` — pre-PR pass.

## Notes for the implementing agent

- The `^6` range already in `web/package.json` means the bump is mostly a
  lockfile resolution + breakage-fix exercise; the version _declaration_ is
  already forward.
- TS 6's most common breakages: stricter `lib.dom` / `lib.es*` signatures,
  removed compiler options (check `tsconfig` for any now-invalid flag), and
  tighter inference on `unknown` / index access. Most `web/` breakage (if any)
  will surface in `web/lib/api.ts`, `web/lib/api/bff.ts`, and the BFF route
  handlers under `web/app/api/`.
- A new error that reveals a real null-handling bug is a _win_ — fix it
  correctly and flag it (AC-7). Do not paper over it; that defeats the purpose
  of the stricter compiler.
- Verify the eslint TS parser (`@typescript-eslint` via `eslint-config-next`)
  tolerates TS 6 — a parser/TS-version mismatch is the most likely
  toolchain-composition failure.
