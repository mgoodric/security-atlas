# 078 — Unblock `npm run lint` after ESLint 10 + eslint-plugin-react incompat

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Surfaced during batch 29 (slices 072 + 073 + 077, continuous-loop iter 3 on 2026-05-15), captured as follow-up per continuous-batch policy. Both engineer-072 and engineer-073 independently observed local `npm run lint` crashes; the breakage didn't block their slices because CI doesn't invoke `npm run lint` directly, but it's a real local-developer pain point that needs a one-shot fix.

**Crash signature** (reproducible on a clean `main` checkout):

```
TypeError: Error while loading rule 'react/display-name': contextOrFilename.getFilename is not a function
    at resolveBasedir (web/node_modules/eslint-plugin-react/lib/util/version.js:31:100)
    ...
```

**Root cause** (verified against `web/package-lock.json` and the upstream package metadata):

- `eslint-plugin-react@7.37.5` (the latest published as of 2026-05-15) declares peer-deps `eslint: '^3 || ^4 || ^5 || ^6 || ^7 || ^8 || ^9.7'` — **no ESLint 10 support**. The plugin uses `context.getFilename()` which ESLint 10 removed in favor of the `context.filename` property.
- `eslint-config-next@16.2.6` (Next.js 16's official lint config bundle) depends on `eslint-plugin-react: "^7.37.0"` — so bumping `eslint-config-next` does not help; it intentionally pins to the still-broken 7.x line.
- Slice 038's ESLint 9 → 10 bump (PR #38 in v1.5.1, commit `3e8734c`) is correct and stays. The plugin incompat is purely upstream.

**Why CI didn't catch it** (load-bearing finding, addressed by AC-3):

- `Frontend · install + build` runs `next build`. Next 16's default behavior on production builds is to lint, BUT the project's CI config doesn't enforce lint as a build gate (and ESLint plugin crashes that happen in dev-mode aren't fatal in production-mode builds when the relevant files compile cleanly).
- `Frontend · vitest` only runs vitest, not ESLint.
- The slice-069 verification suite intentionally scoped frontend lint as **a separate `npm run lint` invocation** (slice 069's `web/testing.md` documents this), but no CI job was wired up to run it. So `npm run lint` is the canonical surface — and that surface is exactly what's broken.

This slice closes both gaps: get `npm run lint` succeeding against `main`, and wire it into CI so the next time an upstream plugin incompat lands, it surfaces immediately instead of being invisible until a contributor runs `npm run lint` locally.

**Remediation paths (the JUDGMENT call for this slice's grill):**

The fix is upstream-dependent. The engineer's grill verifies upstream state **at the time the slice runs** (not at the time this doc was written) and picks one of:

1. **Path A — upstream has shipped.** If `eslint-plugin-react@^7.38` OR `8.x` has shipped that lists `eslint: ^10` in peerDeps, bump it via an `overrides` entry in `web/package.json` (since `eslint-config-next` still pins `^7.37.0`, we override the transitive). Verify `npm run lint` exits 0.
2. **Path B — upstream still incompatible.** Add an `overrides` entry pinning `eslint` to `^9` in `web/package.json` (effectively reverting PR #38's runtime impact while keeping its package.json edit in place). This is a behavior revert, not a history revert. File a follow-on slice (status `not-ready`) titled "re-upgrade ESLint to 10.x once `eslint-plugin-react` ships ESLint 10 support" with the unmet dep listed as "eslint-plugin-react@^7.38+ OR ^8.x with `eslint: ^10` in peerDeps." When that ships upstream, the follow-on becomes `ready` and the override comes back out.
3. **Path C — upstream has a prerelease.** If `eslint-plugin-react` has a `next` / `canary` / `alpha` tag that supports ESLint 10 but isn't on `latest` yet, the engineer makes the judgment call whether to pin to it (decisions log). Generally Path B is safer — wait for the GA — unless the prerelease is well-tested.

The slice doesn't pre-choose; the engineer picks based on what the npm registry actually shows when they run it.

## Acceptance criteria

- [ ] AC-1: `cd web && npm run lint` exits 0 against the merged state. Verified locally before commit; verified in CI by AC-3 after merge.
- [ ] AC-2: One of Path A / B / C from the narrative is applied. The `web/package.json` `overrides` block (or equivalent) carries an inline comment explaining the WHY in one sentence + a date stamp so future readers know it's a workaround.
- [ ] AC-3: New CI job `Frontend · lint` is added to `.github/workflows/ci.yml`. Follows the slice-069 stub-job pattern (path-filter aware, skipped on docs-only changes). Runs `npm run lint` against the `web` workspace. **NOT** added to `.github/branch-protection.json` required-checks initially — lint regressions on every dep bump would flake the merge queue. The job is informational at first; promote-to-required is a future slice if the cadence supports it.
- [ ] AC-4: If Path B is chosen, a follow-on slice `docs/issues/<NNN>-eslint-10-re-upgrade.md` exists with status `not-ready` and the explicit dep "eslint-plugin-react ships ESLint-10-compatible release on `latest`." That slice's body lists the verification command (`npm view eslint-plugin-react peerDependencies`) the maintainer runs to know when to flip it to `ready`.
- [ ] AC-5: CONTRIBUTING.md gains a one-paragraph "Linting" subsection: how to run lint locally (`npm run lint -w web`), where the override lives (link to the line in `web/package.json`), and the path-A/B/C decision (link to this slice's decisions log).
- [ ] AC-6: Verify no OTHER lint-breakage routes exist. Specifically: confirm `npm run typecheck -w web` still passes (it should — TypeScript is unrelated), and confirm `next build` still passes (the `Frontend · install + build` CI job already validates this, so no manual step needed; just inspect the engineer's grill to make sure nothing else regressed).
- [ ] AC-7: A `decisions log` for this slice at `docs/audit-log/078-eslint-10-react-plugin-incompat-decisions.md` records: (1) the upstream state as of slice-run-time (current `eslint-plugin-react` latest version + its peerDeps; whether `8.x` exists; whether a prerelease tag exists), (2) which of Path A/B/C was chosen and why, (3) any follow-on slice number created (AC-4), (4) the inline comment text added to `web/package.json`.
- [ ] AC-8: Pre-commit clean. CI green on required checks. The new `Frontend · lint` job (AC-3) is GREEN on this PR (this slice's deliverable IS making it green).

## Constitutional invariants honored

- **Working norms — Surgical fixes only**: the smallest viable fix per remediation path. Path A is a one-line `overrides` entry. Path B is a one-line `overrides` entry + a follow-on slice. Path C is a one-line `overrides` entry with `@next` / `@canary` semantics. No rewriting of slice 038's history, no broader lint config refactor.
- **AI-assist boundary**: nothing here is AI-generated; this is mechanical config + an upstream-tracking judgment call.

## Canvas references

- _(none — this is build-tooling hygiene, not architectural; canvas doesn't speak to npm dependency management conventions)_

## Dependencies

- **038** (eslint 9 → 10 bump, merged in v1.5.1) — the bump that introduced the latent incompat
- **069** (verification suite — Playwright + vitest + Go coverage) — the slice that established `npm run lint` as a canonical surface in `web/testing.md` but did NOT wire a CI job for it

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT silence the crash by adding `// eslint-disable-next-line react/display-name` or `// @ts-expect-error` to crashing files. The crash is in the rule, not in our code; suppression hides the real problem without fixing it.
- **P0-A2**: Does NOT change `web/eslint.config.ts` to drop `eslint-config-next` from the config array. That's Next.js's recommended baseline; removing it loses real lint signal.
- **P0-A3**: Does NOT modify slice 038's already-merged commit or its CHANGELOG entry. Slice 038 stays. The fix lives in `web/package.json` overrides, not in revisionist history.
- **P0-A4**: Does NOT add the new `Frontend · lint` job to `.github/branch-protection.json` required-checks in this slice. Initial wiring is informational; promotion-to-required is a separate slice once the cadence proves stable.
- **P0-A5**: Does NOT touch other Dependabot PRs open at the time this slice runs (currently #151–#159 + #158 which is `deps(deps-dev): bump eslint from 10.3.0 to 10.4.0`). Those are the maintainer's triage queue, not this slice's surface.

## Skill mix (3–5)

- npm package management — `overrides`, peerDependencies, transitive resolution; reading `npm view` output for upstream state
- ESLint v10 ecosystem — the specific API change (`context.getFilename()` → `context.filename`) and which plugin ecosystems have / haven't shipped support
- CI job design — slice-069's stub-job pattern for path-filter-aware jobs that don't gate the merge queue
- `simplify` — the `web/package.json` override should be a single line with a single comment

## Notes for the implementing agent

- **First action: re-check upstream state.** A lot can change between when this slice doc was written and when it runs. `npm view eslint-plugin-react versions --json | jq '.[-10:]'` shows the last 10 versions; `npm view eslint-plugin-react@latest peerDependencies` shows the current latest's peerDeps. If `eslint: ^10` is now in there, take Path A and the slice is ~0.25d total.
- **The `overrides` entry shape** (Path A example):
  ```json
  "overrides": {
    "eslint-plugin-react": "^X.Y.Z"
  }
  ```
  Goes in `web/package.json` (NOT root `package.json` — the workspace structure matters). Run `npm install -w web` to materialize the override into `web/package-lock.json`. Then `npm run lint -w web` should succeed.
- **The `Frontend · lint` CI job** mirrors slice 069's `Frontend · vitest` job structure. Same runner, same setup steps (checkout + setup-node + npm install + run). The path-filter stub-sibling pattern (slice 061) means it skips on docs-only PRs.
- **Don't get clever on the upstream-tracking follow-on slice (AC-4).** If Path B is chosen, the follow-on is a small, sharp slice: 4 ACs maximum, body under 200 words, status `not-ready`. The maintainer should be able to read the whole thing in 30 seconds and decide whether to promote it.
- **Sanity check after fix**: run `npm run lint -w web 2>&1 | tail -20` and confirm the output is "no warnings or errors" (or some equivalent zero-exit message). If there are real lint errors against the codebase (which there shouldn't be, since this is fixing the plugin crash not new lint failures), fix them or file as separate spillover slices.
