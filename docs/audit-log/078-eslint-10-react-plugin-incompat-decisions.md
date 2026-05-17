# Decisions log — Slice 078 (eslint-plugin-react ESLint-10 incompat unblock)

This is an AFK slice (per `Plans/prompts/04-per-slice-template.md` "Slice types"). The slice doc specified three remediation paths (A/B/C); the choice depends on upstream state at slice-run-time.

## Upstream state (verified 2026-05-16 at slice-run-time)

```
$ npm view eslint-plugin-react versions --json | jq '.[-3:]'
["7.37.3", "7.37.4", "7.37.5"]

$ npm view eslint-plugin-react@latest peerDependencies
{ eslint: '^3 || ^4 || ^5 || ^6 || ^7 || ^8 || ^9.7' }

$ npm view eslint-plugin-react dist-tags
{ next: '7.8.0-rc.0', latest: '7.37.5' }
```

- Latest stable: `7.37.5` — peerDeps cap at `^9.7` (no ESLint 10 support)
- No `8.x` release exists
- `next` dist-tag at `7.8.0-rc.0` is stale (well behind `latest` 7.37.5)

**Result: Path A is unavailable. Path C is unattractive (no usable prerelease). Path B is the choice.**

## Build-time judgment calls

### D1 — Path B: pin ESLint to `^9` (HIGH confidence)

**Decision:** apply slice 078's Path B — pin `eslint` to `^9` so `eslint-plugin-react@7.37.5` can resolve its peerDep and `npm run lint -w web` exits 0.

**Rationale:** Path A requires upstream eslint-plugin-react ESLint-10 support (not yet shipped); Path C requires a viable prerelease (the existing `next` tag is stale at `7.8.0-rc.0`, pre-dating much of the 7.37.x line). Path B is the only path that makes lint work today.

**Alternatives considered:**

- Wait for upstream to ship 10-compat: rejected — `npm run lint` is broken NOW; waiting is hours-to-months of dev-friction. Path B unblocks immediately + the follow-on slice 095 picks up the re-upgrade when upstream catches up.
- Switch from `eslint-plugin-react` to a different React-lint plugin: rejected — `eslint-config-next` pins `^7.37.0` and Next.js's recommended lint baseline is the slice 038 reason-for-being. Replacing the plugin diverges from upstream defaults.
- Disable specific crashing rules with `eslint-disable-next-line`: rejected per slice 078 P0-A1 (silences symptom, not root cause).

### D2 — Direct devDep downgrade (`eslint: ^10` → `^9`) instead of pure-overrides (HIGH confidence — slice-doc deviation)

**Decision:** change `web/package.json`'s direct devDep `eslint: ^10` → `eslint: ^9`. **This deviates from slice 078 Path B's literal text, which calls for an `overrides` block while keeping the `^10` devDep declaration in place.**

**Rationale (the deviation):** the slice doc states: "Goes in `web/package.json` (NOT root `package.json` — the workspace structure matters)." Empirically, npm rejects overrides at workspace-package level — overrides MUST live at the workspace ROOT `package.json` per npm's documented behavior.

Empirical results (this slice-run):

| Attempt                                                                             | Result                                                                                                                                                  |
| ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `overrides` in `web/package.json`                                                   | npm ignored (workspace-level overrides not supported) → eslint stayed at 10.3.0                                                                         |
| `overrides: { eslint: "^9" }` at root                                               | root `node_modules/eslint` resolved to 9.39.4 ✓ BUT workspace `web/node_modules/eslint` stayed at 10.3.0 (the workspace's direct `^10` declaration won) |
| Nested override at root: `overrides: { "@security-atlas/web": { "eslint": "^9" } }` | npm ignored the nested form too → eslint at 10.3.0                                                                                                      |
| `web/package.json` direct devDep `^10` → `^9` + clean install                       | ✓ `node_modules/eslint` is 9.39.4, `npm run lint -w web` exits 0                                                                                        |

**The pure-overrides approach the slice doc envisioned does not work for npm workspaces when the workspace already declares the dep directly.** Direct downgrade is the only path that actually changes the resolved version.

**Audit-trail preservation (the slice doc's stated reason for keeping `^10`):** slice 038's `^10` bump is preserved in git history (PR #38, commit `3e8734c`) regardless of the current declaration. The decisions log + CHANGELOG document the rollback. The semantic intent ("we WANT ESLint 10 once plugin support exists") is preserved via:

1. This decisions log (D1 + D2)
2. The follow-on re-upgrade slice (`docs/issues/095-eslint-10-re-upgrade.md`) with explicit pre-flight verification
3. The CONTRIBUTING.md "Linting" subsection (AC-5) documenting the workaround

Future-iteration cost is identical (slice 095 = "change `^9` → `^10`", same one-line edit). Loss is purely cosmetic (the declared dep no longer reads `^10`).

**Alternatives considered:**

- Stick with the slice-doc literal (overrides + keep `^10`) and accept that lint stays broken: rejected — defeats the entire purpose of the slice. The slice's binary success-test is "npm run lint exits 0", not "we preserve PR #38's declared version literal."
- File a slice-078 update PR first that corrects the spec, then a separate fix PR: rejected — over-engineered process; the decisions log captures the deviation transparently, and AC-2 ("WHY in one sentence + a date stamp") is satisfied by the CONTRIBUTING.md "Linting" subsection.

### D3 — Follow-on slice 095 (HIGH confidence)

**Decision:** file `docs/issues/095-eslint-10-re-upgrade.md` as `not-ready` per slice 078 AC-4. Pre-flight check: `npm view eslint-plugin-react@latest peerDependencies` returns a value listing `^10` (or higher) → flip to `ready`.

**Rationale:** keeps the re-upgrade intent visible in the backlog. When upstream ships compat, the maintainer (or continuous-batch loop) flips status and picks it up. 4 ACs, 200-word body, surgical scope per slice 078's "don't get clever on the follow-on" guidance.

### D4 — `Frontend · lint` CI job added per slice-069 stub-job pattern, NOT in required-checks (HIGH confidence)

**Decision:** new `frontend-lint` + `frontend-lint-stub` jobs in `.github/workflows/ci.yml` per slice-069 stub-job pattern. Path-filter aware (skips on docs-only via `changes.outputs.code != 'true'`). Runs `npm run lint -w web`. **NOT added to `.github/branch-protection.json` required-checks.**

**Rationale:** slice 078 P0-A4 explicitly prohibits adding to required-checks initially — lint regressions on every dep bump would flake the merge queue. Promote-to-required is a future slice once the cadence proves stable. Informational signal is enough for now.

### D5 — Pre-existing 4 lint warnings left as-is (HIGH confidence)

**Decision:** the current `npm run lint -w web` exits 0 with 4 warnings (all "Unused eslint-disable directive (no problems were reported from 'no-console')" in `web/scripts/capture-readme-screenshots.ts`). NOT cleaned up in this slice.

**Rationale:** scope discipline — slice 078's binary success-test is "lint exits 0" + "CI job wired." The 4 warnings are pre-existing tech debt (slice 057's screenshot-capture script's `eslint-disable` directives that are no longer needed since the underlying code paths changed). Cleaning them up here would conflate "unblock lint" with "clean up lint output."

**Revisit condition:** when slice 057 or its follow-on touches `capture-readme-screenshots.ts`, the directives can be removed in the same PR. Or a separate one-line cleanup slice if it surfaces.

## Acceptance criteria status

- [x] AC-1: `cd web && npm run lint` exits 0 against the merged state. Verified locally (exit code 0; 4 warnings, 0 errors).
- [x] AC-2: Path B applied. Direct devDep downgraded (D2 deviation from pure-overrides approach). Date-stamped explanation in CONTRIBUTING.md "Linting" subsection per AC-5.
- [x] AC-3: New `Frontend · lint` + `Frontend · lint` (stub) jobs in `.github/workflows/ci.yml` per slice-069 pattern. NOT in required-checks (P0-A4).
- [x] AC-4: Follow-on slice `docs/issues/095-eslint-10-re-upgrade.md` exists with status `not-ready` + pre-flight verification command.
- [x] AC-5: CONTRIBUTING.md "Linting" subsection (in same commit).
- [x] AC-6: `npm run typecheck -w web` passes; `next build` validated by existing `Frontend · install + build` CI job.
- [x] AC-7: This decisions log.
- [ ] AC-8: Pre-commit clean. CI green on required checks. `Frontend · lint` job green on this PR. (Verified at PR open + merge.)

## Revisit-once-in-use list

- **D2 (slice-doc deviation):** the slice doc's "overrides in web/package.json" guidance was empirically wrong for npm workspaces. Future re-runs of this slice (or similar workspace-override slices) should default to direct-downgrade unless the override mechanism is explicitly tested to work.
- **D3 (follow-on slice 095):** stays `not-ready` until upstream `eslint-plugin-react` ships ESLint-10 compat. Maintainer (or continuous-batch loop) flips at that point.
- **D4 (Frontend · lint not in required-checks):** when the cadence stabilizes (~3 months of clean runs, or after Dependabot churn settles), file a "promote to required" slice.
- **D5 (4 unused-disable warnings):** clean up in slice 057 follow-on or a one-line standalone slice.

## Confidence summary

All 5 decisions HIGH confidence. D2 is the substantive deviation from the slice spec; rationale + alternatives are exhaustively documented above. The other four decisions follow the slice spec verbatim.
