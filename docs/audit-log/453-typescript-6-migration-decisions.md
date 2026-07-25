# Slice 453 — TypeScript 6 migration — decisions log

**Type:** AFK / build-tooling MAJOR (TypeScript 5.9.3 → 6.x across the `web/`
workspace). Supersedes dependabot **#635** (typescript 5.9.3 → 6.0.3).

- detection_tier_actual: none
- detection_tier_target: integration

(No bug surfaced. TS 6.0.3 compiled the entire `web/` source set under
`strict` with **zero** new type errors — no latent null/undefined bug, no
removed-flag breakage, no `lib`-type tightening that the codebase tripped on.
The relevant gate — the `Frontend · lint` + the `next build` typecheck + the
self-host web image build — is the integration tier, and it is green. Had a
breakage existed, that is exactly where it would have been caught; the empty
result is a real, verified outcome, not an unrun check. The
typecheck-integrity canary below proves the gate has teeth, so the green is
load-bearing, not vacuous.)

## State at slice start (the premise moved under us)

The slice doc (and the orchestrator brief) describe the pre-state as:
`web/package.json` **declares** `typescript: "^6"` but the **lockfile pins
5.9.3** — the `^6` range predated a real 6.x release, the inconsistency
dependabot #635 flags.

That premise was true when 453 was filed. It is **no longer the live state**:
slice **450** (vitest 4 paired migration, merged `8589f281`) ran `npm install`,
and npm resolved the standing `^6` range to the now-published **6.0.3** as a
side effect, writing it into `package-lock.json`. So `main` already carries
`node_modules/typescript: 6.0.3` in the lockfile.

**This does NOT make 453 a no-op.** Slice 450 was about vitest; it never
typechecked, lint-checked, or built the `web/` workspace _against TS 6_ with
453's discipline (correct-the-types-not-suppress, prove-no-vacuous-pass,
prove-the-toolchain-composes). The lockfile pin moving is incidental; the
**substance** of 453 — proving TS 6 actually composes with the pinned Next
16.2.7 / React 19.2.7 / @types/react 19.2.17 / eslint-config-next 16.2.6 stack,
and fixing any breakage — remained unverified until this slice. 453 is
therefore a **verification + documentation** slice over an already-resolved
pin: the deliverable is the proof, plus this log and the CHANGELOG record. No
source or lockfile edit was needed because the bump introduced **no** type
errors.

## Versions

- `web/package.json` `typescript` declaration: `^6` (already forward on `main`;
  unchanged by this slice — **AC-1 declaration**).
- `package-lock.json` `node_modules/typescript`: **6.0.3** (already pinned on
  `main` via slice 450's incidental resolution; `npm ci` is faithful;
  `node_modules/.bin/tsc --version` reports **`Version 6.0.3`** — **AC-1
  installed**).
- No `@types/*`, `eslint-config-next`, `@types/react`, `@types/node`, Next, or
  React bump (P0-453-1 satisfied — surgical, in fact zero, dependency delta).

## D1 — TS 6.0.3 compiles the full `web/` source set with zero errors (AC-2/AC-3)

`npm run typecheck -w web` runs **both** configs (`tsc --noEmit &&
tsc --noEmit -p tsconfig.test.json`, the slice-450 split). **Both exit 0.**

- prod `tsconfig.json` (`tsc --noEmit`): exit 0.
- test `tsconfig.test.json` (re-includes `vitest.config.ts` + `**/*.test.ts`):
  exit 0.

No TS-6-introduced error surfaced, so **no type was corrected, narrowed, or
re-annotated** and **no suppression was added** — zero new `as any`,
`@ts-ignore`, or `@ts-expect-error` (AC-2/AC-3, P0-453-2). The codebase already
satisfies TS 6.0.3's stricter inference / removed-deprecation surface. This is
a legitimate clean migration, not a skipped check (see the canary, D3).

## D2 — Non-vacuous typecheck; strict intact (AC-8, P0-453-3)

The threat model's DoS concern is a typecheck that passes **vacuously** (a
dropped strictness flag silently disabling a check). Disproved:

- `tsc --noEmit --listFiles` over the prod config processes **447** `web/`
  source files (excluding `node_modules`) — a non-zero, meaningful set
  including `lib/api/base.ts`, the BFF route handlers under `app/api/`, and the
  `(authed)` page modules.
- `tsc --showConfig` reports `strict: true`, `noEmit: true`, `target: es2017` —
  the strict gate is intact and unmodified.
- `git diff origin/main` over `web/tsconfig.json` + `web/tsconfig.test.json` is
  **empty** — no tsconfig edit, so **no TS-6-mandated change** was required
  (AC-8 satisfied trivially; no opportunistic loosening, no opportunistic
  tightening).
- `tsc --noEmit` emits **no** deprecated-flag / removed-option diagnostic
  (no `TS5xxx` "no longer supported" / "unknown compiler option") — every
  option in the existing `tsconfig.json` remains valid under TS 6.0.3. (The
  config sets no flags that TS 6 removed; the common TS-6 casualties —
  `noImplicitUseStrict`, `keyofStringsOnly`, `suppressImplicitAnyIndexErrors`,
  `out`, `noStrictGenericChecks` — are none of them present here.)

## D3 (LOAD-BEARING) — typecheck-integrity canary: the gate has teeth

To prove the green typecheck is real (not a misconfigured no-op), a **scratch,
uncommitted** type violation was injected into
`web/app/(authed)/controls/count-label.ts`:

```ts
const __canary: number = "not a number";
```

`tsc --noEmit` (prod config) then **FAILED**:

```
app/(authed)/controls/count-label.ts(82,9): error TS2322: Type 'string' is not assignable to type 'number'.
PROD TYPECHECK EXIT: 2
```

The scratch edit was reverted (`git checkout --`), `git status` confirmed
clean, and `tsc --noEmit` returned to exit 0. **The typecheck genuinely runs
and catches a deliberate error under TS 6.0.3** — P0-453-3 disproved; the
slice's whole point (the gate still guards) holds.

## D4 — Ecosystem composition verified (AC-4)

The defining risk: TS 6 must compose with the pinned, OUT-OF-SCOPE-to-bump
stack. Verified, no hard incompatibility:

- **eslint-config-next@16.2.6 + @typescript-eslint parser on TS 6.0.3:**
  `npm run lint -w web` exits **0** — the `@typescript-eslint` TS parser
  (`8.59.3` in the lockfile) does **not** crash on or reject TS 6.0.3; it parses
  the whole `web/` tree. (Two pre-existing `no-console` unused-disable
  **warnings** in `scripts/capture-readme-screenshots.ts` are unchanged from
  `main` — `git diff origin/main` over that file is empty — NOT TS-6-introduced,
  warnings-not-errors, and CI's bare `npm run lint` exits 0 on warnings. Out of
  scope per P0-453-4; not touched.)
- **@types/react@19.2.17 + Next 16.2.7 bundled `.d.ts`:** `next build` (which
  typechecks the full prod tsconfig graph, including Next's and React's bundled
  declaration files) reaches **`✓ Compiled successfully`** under TS 6.0.3 — the
  React 19 + Next 16 type definitions compile cleanly under the TS 6 checker.

No `STOP-AND-REPORT` ecosystem blocker was hit. No unrelated dep was bumped or
forced (P0-453-1).

## D5 — vitest 4 coexists with TS 6 (AC-6)

`npm run test -w web` (vitest 4.1.8, on `main` from slice 450) runs **184 test
files / 1760 tests, all passing** under the TS-6 toolchain. The type fixes were
zero, so there is trivially no behaviour change; the suite is the
no-behaviour-change witness regardless. (Playwright e2e is a CI-only tier here;
the `next build` + vitest green plus the unchanged source set establish
no-behaviour-change — the e2e job runs in CI on the PR.)

## D6 (LOAD-BEARING) — self-host web image builds under TS 6 (the slice-450 lesson)

Slice 450 established that `deploy/docker/web.Dockerfile` runs `npm ci` then
`npm run build` (`next build`), which typechecks the production tsconfig graph —
so a TS-graph error anywhere in the build set breaks the `Self-host bundle ·
end-to-end` CI jobs. Reproduced the CI self-host gate exactly:

```
docker build -f deploy/docker/web.Dockerfile -t sa-web-test-453 .
→ #15 next build: ✓ Compiled successfully
→ #19 naming to docker.io/library/sa-web-test-453:latest done
AUTHORITATIVE DOCKER EXIT: 0
```

The full multi-stage image (deps → builder `next build` under TS 6.0.3 →
standalone runtime) builds end-to-end. The highest-risk gate is green. (Test
image removed after the run.)

## Scope discipline (P0s)

- **P0-453-1** — zero dependency bumps (no eslint-config-next / @types/react /
  @types/node / Next / React change). The TS pin was already at 6.0.3 on `main`.
- **P0-453-2** — zero suppressions added (no type error to suppress).
- **P0-453-3** — typecheck proven non-vacuous (447 files, strict on) and
  proven to catch a deliberate error (D3 canary).
- **P0-453-4** — no typed code refactored; the source tree is byte-identical to
  `main` (the only committed changes are this log + the CHANGELOG bullet).

## Verification summary (local CI parity)

| Gate                  | Command                                        | Result                                              |
| --------------------- | ---------------------------------------------- | --------------------------------------------------- |
| AC-1 tsc version      | `tsc --version`                                | `Version 6.0.3`                                     |
| AC-2 typecheck (both) | `npm run typecheck -w web`                     | exit 0 / exit 0                                     |
| AC-3 no suppressions  | grep diff                                      | zero `as any`/`@ts-ignore`/`@ts-expect-error` added |
| AC-4 lint             | `npm run lint -w web`                          | exit 0 (parser tolerates TS6)                       |
| AC-5 build            | `npm run build -w web`                         | `✓ Compiled successfully`                           |
| AC-6 vitest           | `npm run test -w web`                          | 184 files / 1760 tests pass                         |
| AC-8 tsconfig         | `git diff origin/main`                         | empty (no mandated change)                          |
| Canary                | scratch bad-type → `tsc --noEmit`              | `error TS2322`, exit 2 (then reverted)              |
| Self-host             | `docker build -f deploy/docker/web.Dockerfile` | `✓ Compiled successfully`, exit 0                   |

## Found-defect record (AC-7)

**None.** TS 6.0.3 surfaced no latent null/undefined bug. (Documented as a real
outcome of the clean compile, not an unrun check — see D1/D3.)
