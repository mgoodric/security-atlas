# 450 — vitest 4 + @vitest/coverage-v8 4 paired migration

**Cluster:** Infra
**Estimate:** M (1-2d)
**Type:** AFK

**Status:** `ready`

## Narrative

Dependabot has filed **two** PRs that each move exactly one half of a pair
that must move together: **#948** (`vitest` 2.x → 4.x) and **#950**
(`@vitest/coverage-v8` 2.x → 4.x). Each PR fails the `Frontend · vitest` CI
job with `Running mixed versions is not supported` — because `vitest` and its
coverage provider are version-locked and dependabot bumps them independently.
Neither can merge alone; the fix is one PR that bumps **both** to 4.x.

`vitest` 2 → 4 is two majors. The load-bearing migration concern is **Vite 6**:
vitest 4 rides Vite 6, whose ESM-only config API and changed coverage-provider
option surface can break `web/vitest.config.ts`. That config pins
`environment: "node"` (slice 069 P0-A3 — vitest is the node-only
module-logic tier: BFF route handlers, `lib/api.ts`, `lib/api/bff.ts`; no JSX,
no DOM — per CLAUDE.md "Component-test surface (Q-3 — decided OUT of scope)").
That `node` environment pin must survive the migration unchanged.

The second load-bearing concern is the **coverage gate**: slice 347 installed
107 per-file coverage floors backed by a JSON sidecar (mirroring the Go-side
gate). A coverage-provider misconfiguration during a v8-provider major bump
could silently _drop_ the gate — coverage "passes" because nothing is being
measured. This slice must assert the merged-coverage gate still **enforces**
(an intentionally-uncovered line still fails the floor), not merely that the
suite is green.

This slice **supersedes dependabot PRs #948 and #950**.

**Scope discipline.** Tooling bump + config migration only. No new test files,
no floor changes (the slice 347 ratchet is monotonic and owned by floor-lift
slices), no `environment` change, no React-component-test introduction (still
out of scope for v1).

## Threat model

STRIDE pass. This is test tooling — the runtime-security surface is minimal —
but the **integrity of the coverage gate itself** is the real threat: a
silently-disabled gate is a supply-chain / quality regression that hides future
defects.

**S — Spoofing / T — Tampering / R — Repudiation / I — Information disclosure /
E — Elevation of privilege**

- _Threat:_ None of these apply to a test-runner bump in any direct way. vitest
  runs only at build/CI time against `web/` module logic; it ships no runtime
  code and touches no tenant data, no auth path, no RLS.
- _Mitigation:_ Confirm the bump touches only `web/` devDependencies +
  `web/vitest.config.ts`; no production `web/app` or `web/lib` runtime code
  changes.

**D — Denial of service (the real risk, reframed: gate-integrity)**

- _Threat 1 — silent gate drop._ A coverage-provider option rename/removal in
  the v8 provider major causes coverage collection to no-op or to exclude the
  measured set, so the 107 per-file floors "pass" against zero data. Future
  uncovered code merges undetected.
- _Mitigation:_ Add/confirm a meta-assertion: an intentionally-uncovered branch
  in a throwaway fixture (or a deliberate temporary floor bump in a scratch run)
  causes the gate to FAIL. Verify the coverage-summary JSON the gate reads is
  populated with real per-file numbers post-migration, not empty.
- _Anti-criterion:_ P0-450-2, P0-450-3.
- _Threat 2 — config breakage masks suite._ A Vite-6 ESM-config error could
  make vitest exit 0 with zero tests collected (false-green).
- _Mitigation:_ Assert the post-migration run reports the expected
  non-zero test count (the full existing suite still runs).
- _Anti-criterion:_ P0-450-4.

## Acceptance criteria

- [ ] **AC-1.** `web/package.json` bumps BOTH `vitest` `^2` → `^4` and
      `@vitest/coverage-v8` `^2` → `^4` in the same change; versions match
      (no mixed-version error).
- [ ] **AC-2.** `web/vitest.config.ts` migrated to the Vite 6 / vitest 4 config
      API; `environment: "node"` retained unchanged (slice 069 P0-A3).
- [ ] **AC-3.** `npm install` from repo root succeeds; lockfile updated.
- [ ] **AC-4.** `npm run test -w web` (i.e. `vitest run`) exits 0 and reports
      the same non-zero test count as before the bump (full suite still runs;
      no silent zero-collection).
- [ ] **AC-5.** Coverage run (`vitest run --coverage`) produces a populated
      per-file coverage summary JSON; the slice 347 107-floor gate re-runs and
      passes against **real** measured numbers.
- [ ] **AC-6.** Gate-enforcement proof: a deliberately-uncovered line (verified
      in a scratch/throwaway run, not committed) is shown to FAIL the floor —
      i.e. the gate still has teeth. Evidence recorded in the PR body.
- [ ] **AC-7.** `Frontend · vitest` CI job is green; the
      `coverage-summary.json` artifact upload still works.
- [ ] **AC-8.** No production `web/app` / `web/lib` runtime code changed; diff
      is confined to `web/package.json`, `web/package-lock.json` churn, and
      `web/vitest.config.ts` (plus any test-helper config shims).
- [ ] **AC-9.** `pre-commit run --all-files` passes. PR body notes "Supersedes
      #948 and #950".

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md "four enforced surfaces" + Q-3).** vitest
  stays the node-only module-logic tier; the migration preserves the tier
  boundary and the slice 347 coverage ratchet.

## Canvas references

- CLAUDE.md "Testing discipline" + "Component-test surface (Q-3 — decided OUT
  of scope)".
- Slice 069 (vitest tier bootstrap, `environment: node` pin).
- Slice 347 (vitest 107 per-file coverage-floor JSON sidecar).

## Dependencies

- Supersedes **dependabot #948** (vitest 2 → 4) and **#950**
  (@vitest/coverage-v8 2 → 4).
- **#069** (vitest tier) and **#347** (coverage ratchet) — both `merged`; this
  slice must preserve both.

## Anti-criteria (P0 — block merge)

- **P0-450-1.** Does NOT bump vitest and coverage-v8 to different majors
  (the mixed-version failure mode is the whole reason this slice exists).
- **P0-450-2.** Does NOT silently disable or weaken the slice 347 coverage gate —
  the gate must enforce against real measured per-file numbers.
- **P0-450-3.** Does NOT lower any of the 107 per-file floors (the ratchet is
  monotonic; floor changes belong to floor-lift slices, not a tooling bump).
- **P0-450-4.** Does NOT allow a false-green (zero tests collected / zero
  coverage measured) to pass; AC-4 + AC-5 assert real counts.
- **P0-450-5.** Does NOT change `environment` from `"node"` or introduce
  React-component (DOM) tests — out of scope for v1.

## Skill mix (3-5)

- `dependency-auditor` — read the vitest 3 + 4 + Vite 6 migration guides.
- `tdd` — re-run the existing vitest suite as the regression gate.
- `ci-cd-pipeline-builder` — confirm the `Frontend · vitest` job + artifact
  upload still work.
- `simplify` — pre-PR pass.

## Notes for the implementing agent

- The two dependabot PRs each fail with the literal
  `Running mixed versions is not supported` message — that is the signal that
  they must be merged as one. Close both in favor of this slice's PR.
- Vitest 4 rides Vite 6; the most likely breakages are: (1) ESM-only config
  (`vitest.config.ts` already TS/ESM, so low risk), (2) `coverage.provider`
  option renames, (3) the `coverage.reporter` set needed to emit the
  `json-summary` the slice 347 gate reads. Verify the gate's input file path
  and shape are unchanged.
- The gate-enforcement proof (AC-6) is the load-bearing AC — a green suite is
  not sufficient evidence the gate survived. Demonstrate teeth, then revert the
  scratch change.
