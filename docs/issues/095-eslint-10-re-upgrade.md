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
