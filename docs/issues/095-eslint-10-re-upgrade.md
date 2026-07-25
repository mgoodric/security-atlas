# 095 — Re-upgrade ESLint to 10.x once `eslint-plugin-react` ships compatible release

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK

## Narrative

Surfaced during slice 078, captured as follow-up per continuous-batch policy.

Slice 078 chose Path B: pinned ESLint to `^9` in `web/package.json` because `eslint-plugin-react@7.37.5` (the latest stable on 2026-05-16) caps its peerDeps at `^9.7` and lacks an ESLint 10-compatible release. The `next` dist-tag was stale at `7.8.0-rc.0`.

When upstream ships ESLint 10 support, this slice re-upgrades the project:

1. Verify upstream state: `npm view eslint-plugin-react@latest peerDependencies` returns a list that includes `^10` (or `^11` / `^12` if eslint bumped further).
2. Bump `web/package.json` `eslint: ^9` → the current ESLint major (probably `^10`, may be later).
3. Run `npm install` from repo root, run `npm run lint -w web` — confirm exits 0 with no plugin crash.
4. Commit the bump.

## Pre-flight verification command

```bash
npm view eslint-plugin-react@latest peerDependencies
```

The maintainer (or continuous-batch loop) flips this slice's status from `not-ready` to `ready` when that command returns a peerDeps value listing `^10` (or higher).

## Acceptance criteria

- [ ] AC-1: Pre-flight verification — `eslint-plugin-react@latest`'s peerDeps include the ESLint major declared in `web/package.json` target. If not, exit cleanly with a one-paragraph PR-body note and keep status `not-ready`.
- [ ] AC-2: `web/package.json` `eslint: ^9` → current major (probably `^10`). No other devDeps touched in this slice.
- [ ] AC-3: `npm install` from repo root + `npm run lint -w web` exits 0. CI's `Frontend · lint` job (added in slice 078) is green on this PR.
- [ ] AC-4: Pre-commit clean. Conventional Commit `fix(infra): re-upgrade ESLint to ^10 (eslint-plugin-react now compat) (#095)`.

## Constitutional invariants honored

- **Working norms — Surgical fixes**: one-line `web/package.json` edit + re-install. No lint config refactor.
- **AI-assist boundary**: nothing AI-generated.

## Dependencies

- **078** (eslint-plugin-react incompat unblock, merged) — established the pin + the `Frontend · lint` CI gate
- **Upstream:** `eslint-plugin-react` ships a release with `^10` (or higher) in its peerDeps

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT run if pre-flight fails. Stay `not-ready` until upstream actually ships compat.
- **P0-A2**: Does NOT introduce ESLint config changes (`web/eslint.config.ts`). Pin-bump only.
- **P0-A3**: Does NOT promote `Frontend · lint` to required-checks in this slice. That's a separate cadence-stability decision.

## Notes for the implementing agent

- **First action** is the `npm view eslint-plugin-react@latest peerDependencies` check. If the value still caps at `^9.x`, this slice is still `not-ready` — exit immediately without making any edits.
- **The slice is 5 lines of code change** (one in `web/package.json`, the rest is `package-lock.json` churn from `npm install`). Should land in under 15 minutes once upstream is ready.
- **Don't bump `eslint-config-next`** or other deps in this slice unless absolutely necessary. Surgical re-upgrade only.

## Re-check log

Each entry records a pre-flight run of the AC-1 verification command. The slice stays `not-ready` until an entry shows a compatible major.

### 2026-07-25 — NOT READY (no dependency change made)

```
$ npm view eslint-plugin-react@latest peerDependencies
{ eslint: '^3 || ^4 || ^5 || ^6 || ^7 || ^8 || ^9.7' }

$ npm view eslint-plugin-react@latest version
7.37.5

$ npm view eslint-plugin-react dist-tags
{ next: '7.8.0-rc.0', latest: '7.37.5' }

$ npm view eslint@latest version
10.8.0
```

Unchanged from slice 078's 2026-05-16 reading: `latest` is still `7.37.5`, still capped at `^9.7`, and the `next` dist-tag is still the same stale `7.8.0-rc.0` prerelease. `7.37.5` remains the highest version published on the 7.x line — every `7.37.x` declares the identical `^9.7` cap, so there is no untagged newer release to reach for either.

**The gate is wider than this slice assumed.** `eslint-plugin-react` is not the only ESLint-9-capped plugin `eslint-config-next@16.2.9` pulls in. Current `latest` peerDeps:

| Plugin                      | peerDeps `eslint` range                                          | ESLint 10 OK? |
| --------------------------- | ---------------------------------------------------------------- | ------------- |
| `eslint-plugin-react`       | `^3 \|\| ^4 \|\| ^5 \|\| ^6 \|\| ^7 \|\| ^8 \|\| ^9.7`           | No            |
| `eslint-plugin-import`      | `^2 \|\| ^3 \|\| ^4 \|\| ^5 \|\| ^6 \|\| ^7.2.0 \|\| ^8 \|\| ^9` | No            |
| `eslint-plugin-jsx-a11y`    | `^3 \|\| ^4 \|\| ^5 \|\| ^6 \|\| ^7 \|\| ^8 \|\| ^9`             | No            |
| `eslint-plugin-react-hooks` | `^3.0.0 \|\| ... \|\| ^9.0.0 \|\| ^10.0.0`                       | Yes           |

Three of the four must ship ESLint 10 support before the pin comes out, not just `eslint-plugin-react`. AC-1's verification command should be read as necessary-but-not-sufficient; re-check all three.

**Failure mode is silent, not loud.** npm 11.14.0 does _not_ hard-fail this upgrade with `ERESOLVE`. An isolated install of `eslint@10.6.0` + `eslint-config-next@16.2.9` exits 0 and produces a tree that `npm ls` then reports as invalid:

```
npm error invalid: eslint@10.6.0 /private/tmp/.../node_modules/eslint
```

...with `eslint@10.6.0 deduped invalid` against all three plugin peer ranges above. So a green `npm install` is not evidence the upgrade is safe — the unmet peers surface as the slice 078 runtime crash (`contextOrFilename.getFilename is not a function`), not as an install error. Verify with `npm ls eslint` after any future attempt.

**Disposition of Dependabot PR #1435** (`deps(deps-dev): bump eslint from 9.39.4 to 10.6.0`, branch `dependabot/npm_and_yarn/eslint-10.6.0`, open): **not superseded — must stay unmerged.** It proposes exactly the bump this slice's pre-flight just rejected, and it carries no companion plugin bump (none exists to carry). Merging it would reproduce the slice 078 crash. It stays open as the standing tracker for the upgrade, or the maintainer closes it with `@dependabot ignore this major version`; either way it does not merge until this slice's re-check passes. That call is the maintainer's triage decision, consistent with slice 078 P0-A5.

No `--legacy-peer-deps`, no `overrides` entry, and no peer-warning suppression was used or added. `web/package.json` and `package-lock.json` are unchanged by this re-check.
