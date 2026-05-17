# Slice 083 — Pre-push hook: add `npm run lint -w web` — decisions log

**Slice type:** `AFK` (the five ACs are mechanically verifiable). This log records the build-time judgment calls the slice surfaced. None of these blocked merge.

## Decisions made

### D1 — Option A: `local` `repos:` entry in `.pre-commit-config.yaml` with `stages: [pre-push]`

AC-2 offered three mechanisms. Picked **Option A** verbatim from the slice spec.

Rationale:

- **Matches slice 081 D3's prescribed re-enable path** ("the lint invocation would be added as a local-stage hook in `.pre-commit-config.yaml` with `stages: [pre-push]` and `entry: npm run lint -w web`"). Slice 081 already did the design thinking; this slice just executes it.
- **Smallest viable change** (P0 — "Working norms — Surgical fixes"): one `local` `repos:` entry, mirroring the existing `gofmt` block's pattern (`language: system`, no upstream rev pin, no extra wrapper script).
- **No new files, no new install steps.** The existing `just install-hooks` recipe already runs `pre-commit install --hook-type pre-push --install-hooks`; pre-commit-framework discovers the new `stages: [pre-push]` entry automatically. Engineers on `main` who already have hooks installed pick this up on next push with zero action.
- Options B (wrapper script) and C (separate hook installer) would each have added a moving part outside `.pre-commit-config.yaml` — strictly less observable and harder to remove later.

**Confidence: high.**

### D2 — `files: ^web/` triggers the hook only when web/ files are part of the push

The pre-commit-framework `files:` regex gates whether the hook runs at all for a given changeset. `^web/` matches the smallest meaningful scope:

- A push touching only Go (no `web/` files) skips `npm-lint-web` entirely (`(no files to check)Skipped`) — preserves fast iteration cycles for backend-only work.
- A push touching any file under `web/` runs the full workspace lint (`pass_filenames: false`, since `npm run lint -w web` ignores per-file lists and applies its own ESLint globs).

Trade-off considered: omitting `files:` would always run lint on every push. Rejected: ~3-second floor on backend-only pushes is gratuitous when the CI `Frontend · lint` job is already path-gated by slice 061's pattern.

**Confidence: high.** Matches the slice spec's explicit `files: ^web/` directive.

### D3 — `language: system` (not `language: node`)

Mirrors the existing `gofmt` block. We do NOT want pre-commit-framework to bootstrap its own isolated node environment — the worktree already has `node_modules/` installed for development; reusing it is correct and avoids second-copy overhead.

Side-effect: the hook fails closed if a contributor hasn't run `npm install` yet. That's the desired behavior — frontend lint can't run without deps, and `npm: command not found` is a far better error than a silently-skipped check.

**Confidence: high.**

### D4 — Did NOT add to root `npm run lint` (P0-A3 honored)

P0-A3 explicitly forbids the workspace-root variant. Used `npm run lint -w web` per slice spec wording. Verified `web/package.json` exposes `"lint": "eslint"` which is the same command the new `Frontend · lint` CI job (slice 078) runs — local + CI now invoke identical lint.

**Confidence: high.**

### D5 — Did NOT touch `--no-verify` (P0-A1 honored)

P0-A1 requires `git push --no-verify` to keep working. Git's pre-push hook is bypassed by `--no-verify` at the git layer — no work required on our end. Verified by inspection of the slice 081 decisions log D4 (same bypass semantics, same reasoning).

**Confidence: high.**

## AC-3 deliberate-failure test

**Procedure:**

1. Staged the new `.pre-commit-config.yaml` (with the `npm-lint-web` hook) + a deliberate change to `web/app/page.tsx`: `const _broken = ["a", "b"].map((x) => <span>{x}</span>);` — missing `key` prop in a `.map()`, which `react/jsx-key` is configured as `error` by `next-ts` ESLint config.
2. Committed with `git -c commit.gpgsign=false commit --no-verify -s -m "test: AC-3 deliberate eslint-error fixture (slice 083)"` (--no-verify because the pre-commit suite would have caught nothing here, but skipping it removed all variables from the test).
3. Ran `git push --dry-run origin HEAD:refs/test/083-prepush-deliberate`.

**Result: BLOCKED. Hook fired and exited 1. Verbatim output (excerpt):**

```
trim trailing whitespace.................................................Passed
fix end of files.........................................................Passed
check yaml...............................................................Passed
... (other pre-commit hooks all Passed or Skipped) ...
prettier.................................................................Passed
npm-lint-web.............................................................Failed
- hook id: npm-lint-web
- exit code: 1

> @security-atlas/web@0.0.0 lint
> eslint

/Users/gmoney/Development/security-atlas-083/web/app/page.tsx
  6:9   warning  '_broken' is assigned a value but never used  @typescript-eslint/no-unused-vars
  6:41  error    Missing "key" prop for element in iterator    react/jsx-key

✖ 6 problems (1 error, 5 warnings)

npm error Lifecycle script `lint` failed with error:
npm error code 1
...
error: failed to push some refs to 'https://github.com/mgoodric/security-atlas.git'
```

`git push` exit code: **1**. Push did not transmit. Verified `--no-verify` still bypasses (sanity-checked separately during slice 081 testing; not re-tested here since the bypass code path is git-native and slice 083 changes nothing about it).

4. Reverted the deliberate test commit with `git reset --hard HEAD~1`, then re-applied the two intentional file edits (`.pre-commit-config.yaml` + `CONTRIBUTING.md`) which had been collateral damage of the reset.

**AC-3 status: PASS.**

## Surprises surfaced

- **`@typescript-eslint/no-unused-vars` is configured as `warning`, not `error`.** First attempt at the deliberate fixture (an unused `const`) printed the warning but lint exited 0 — the hook would NOT have blocked. Re-tried with `react/jsx-key` (which next-ts ships as `error`) and the hook blocked correctly. Worth knowing for anyone debugging "why didn't lint catch X locally?" — the answer is severity, not coverage. No action: this is the upstream `next-ts` config's choice and outside slice 083's scope.
- **`git reset --hard HEAD~1` discarded the staged `.pre-commit-config.yaml` edit alongside the test fixture.** Expected git behavior (`--hard` resets the index + working tree to the target commit); just had to re-apply the edit. Noted here so future AC-3-style tests stash the production edits separately before introducing the deliberate-failure commit, or use a dedicated test branch.
