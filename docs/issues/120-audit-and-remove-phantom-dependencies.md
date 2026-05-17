# 120 — Audit and remove phantom (unused) dependencies across all manifests

**Cluster:** Quality (CI hygiene)
**Estimate:** 1-2d
**Type:** JUDGMENT

## Narrative

Surfaced 2026-05-16 during the `/loop dep-review` analysis of PR #154 (lucide-react 0.475.0 → 1.16.0): `lucide-react` is declared in `web/package.json` line 26 but has **zero** TypeScript imports anywhere in `web/`. It's a phantom dependency — declared in the manifest, never imported in source.

This pattern is almost certainly not unique to lucide-react. Modern frontend scaffold tooling (shadcn/ui templates + create-next-app + Tailwind starter) tends to bring in icon libs, animation libs, and util libs that get pinned in `package.json` but never imported when the actual UI is hand-built differently from the template. Similar drift can land in `oscal-bridge/pyproject.toml` (uv adds transitive workspace tools), `go.mod` (Go's compiler catches direct phantoms but indirect drift after a file delete needs `go mod tidy`), and `docs-site/requirements.txt`.

This slice ships:

1. **An audit script** (`scripts/audit-deps.sh` or equivalent) that classifies every direct dependency across all four manifests (`web/package.json`, `go.mod`, `oscal-bridge/pyproject.toml`, `docs-site/requirements.txt`) as USED / USED-VIA-CONFIG / USED-VIA-SCRIPT / PHANTOM. The script is reproducible — same input = same output — and runnable both locally and in CI.

2. **Removal of every PHANTOM identified by the initial run.** One commit per ecosystem (max 4 commits), each with a 1-paragraph rationale in the commit body. Post-removal, the full CI suite passes on every commit (no broken builds).

3. **A recurring-cadence mechanism.** Implementing engineer's JUDGMENT call across four documented options; rationale recorded in `docs/audit-log/120-audit-and-remove-phantom-dependencies-decisions.md`. The maintainer's lean (per the surfacing conversation) is **option (b) — PR-comment CI check on manifest changes** — but the engineer can pick differently with documented justification.

Out of scope: indirect transitive dependency cleanup (that's a different problem domain — `npm dedupe`, `go mod tidy`, etc., each have their own tradeoffs). Out of scope: licensing audit (separate slice if needed). Out of scope: vulnerability scanning (Dependabot + Advisory DB already cover that surface).

## Threat model

| STRIDE                       | Threat                                                                                                                                      | Mitigation                                                                                                                                                                                                                                                                                             |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **S** Spoofing               | None — no auth surface, no new endpoints                                                                                                    | n/a                                                                                                                                                                                                                                                                                                    |
| **T** Tampering              | Audit incorrectly classifies a config-only dep (eslint plugin, postcss plugin, tailwind plugin) as phantom → removal breaks the build       | AC-3 mandates a "USED-VIA-CONFIG" allowlist covering `.eslintrc*` / `eslint.config.*`, `.prettierrc*`, `postcss.config.*`, `vitest.config.*`, `playwright.config.*`, `tailwind.config.*`, `next.config.*`, `mkdocs.yml`. AC-7 requires CI green on every removal commit (no broken builds in history). |
| **R** Repudiation            | None — slice doesn't write to audit logs or tenant data                                                                                     | n/a                                                                                                                                                                                                                                                                                                    |
| **I** Information disclosure | The audit script's output enumerates what tooling was considered + rejected — low concern in OSS repo where all manifests are public anyway | n/a                                                                                                                                                                                                                                                                                                    |
| **D** Denial of service      | Audit script could be slow on large repos                                                                                                   | AC-5: script MUST complete in < 30s on the current tree. CI cadence (if option b) is bounded by the same 30s budget.                                                                                                                                                                                   |
| **E** Elevation of privilege | None — no role boundaries crossed                                                                                                           | n/a                                                                                                                                                                                                                                                                                                    |

**Anti-criteria additions from threat model:** P0-A4 (the config-allowlist requirement) is load-bearing — incorrectly classifying eslint plugins as phantom and removing them would silently disable lint checks. The slice MUST enumerate the allowlist explicitly in the audit script, not as a TODO comment.

## Acceptance criteria

### Audit script

- [ ] AC-1: A reproducible audit script lands at `scripts/audit-deps.sh` (or `scripts/audit-deps.ts` / `.py` — engineer's choice; bash is the lowest-friction default). Same git tree state + same script invocation produces identical output (no non-determinism from filesystem ordering, no time-dependent classification).
- [ ] AC-2: The script classifies every direct dependency in all four manifests (`web/package.json`, `go.mod`, `oscal-bridge/pyproject.toml`, `docs-site/requirements.txt`) as one of:
  - `USED` — appears in an `import` / `require` / `from X import Y` in non-test, non-lockfile source files
  - `USED-VIA-CONFIG` — appears in one of the allowlisted config files (see AC-3)
  - `USED-VIA-SCRIPT` — appears as a CLI invocation in `package.json` `scripts:` block, `justfile` recipes, or `.github/workflows/*.yml` run-steps
  - `PHANTOM` — none of the above
- [ ] AC-3: The "USED-VIA-CONFIG" allowlist explicitly covers (extend if other config files exist): `.eslintrc*`, `eslint.config.*`, `.prettierrc*`, `postcss.config.*`, `vitest.config.*`, `playwright.config.*`, `tailwind.config.*`, `next.config.*`, `tsconfig*.json`, `mkdocs.yml`, `.pre-commit-config.yaml`, `pyproject.toml` `[tool.*]` sections.
- [ ] AC-4: Script output is structured — emits one row per classified dep with columns: `ecosystem`, `package`, `classification`, `evidence` (file:line for USED / USED-VIA-CONFIG / USED-VIA-SCRIPT; empty for PHANTOM). Format: `tsv` to stdout for ease of `awk`/`cut` downstream.
- [ ] AC-5: Script completes in `< 30s` on the current repo. Use `ripgrep` (already in CI) for the source scan, not `grep -r` (faster).
- [ ] AC-6: Script has a `--ecosystem <npm|go|pip-bridge|pip-docs>` flag for scoped runs (useful in CI when only one manifest changed).

### Initial removal pass

- [ ] AC-7: All deps identified as `PHANTOM` by the initial audit run are removed. One commit per ecosystem; each commit body lists the removed packages + a 1-line "no consumers found" justification. CI passes on every commit (no broken builds in the squash-merged history).
- [ ] AC-8: For any dep flagged PHANTOM that the engineer chooses to **keep** (e.g. "planned for next sprint"), document the keep-decision in the slice's decisions log with the package name + the future-use rationale + a tracking-issue ref or follow-up slice number. Don't silently skip.

### Recurring cadence

- [ ] AC-9: Implementing engineer picks ONE of these four options and ships it:
  - **(a)** Periodic AFK slice — re-run the audit every N weeks; file removals as a follow-up slice. Lowest friction; highest latency.
  - **(b)** **(Maintainer's lean)** PR-comment CI check on manifest changes — when `web/package.json` / `go.mod` / `oscal-bridge/pyproject.toml` / `docs-site/requirements.txt` changes in a PR, a CI job posts a comment listing any new PHANTOM candidates. Comment-only, non-blocking.
  - **(c)** Pre-commit hook on manifest change — blocks the commit if a PHANTOM exists. Most aggressive; highest contributor friction.
  - **(d)** Leave manual — `scripts/audit-deps.sh` is the entry point; maintainer runs it quarterly. Lowest tooling overhead.
- [ ] AC-10: Decisions log at `docs/audit-log/120-audit-and-remove-phantom-dependencies-decisions.md` documents WHICH option was chosen + WHY (the tradeoffs the engineer weighed, not just the choice). The four-option enumeration MUST appear with each one's pro/con even if not chosen — this is the doc that a future contributor will read to revisit the choice.

### CONTRIBUTING.md

- [ ] AC-11: `CONTRIBUTING.md` gets a "Dependency hygiene" subsection (3-5 sentences) pointing to `scripts/audit-deps.sh` + the chosen cadence mechanism. New contributors should know what triggers a phantom-check and how to investigate the result.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — the audit script is additive; the removal pass is per-ecosystem-commit (not "remove everything in one mega-commit"); the recurring mechanism is the engineer's single picked option, not a kitchen-sink of all four
- **CLAUDE.md "No backwards-compatibility hacks"** — when removing a phantom, just remove it; don't leave a "// removed for slice 120" comment or a re-export shim
- **CLAUDE.md style** — no emojis, Conventional Commits, Co-Authored-By trailer, DCO sign-off

## Canvas references

- `Plans/canvas/09-tech-stack.md` (the manifest layout this slice audits)
- `web/package.json`, `go.mod`, `oscal-bridge/pyproject.toml`, `docs-site/requirements.txt` (the four manifests)
- `Plans/prompts/08-dependabot-pr-review.md` (the loop that surfaced the phantom-dep observation in the first place — STEP 3 "Find call sites" is the spiritual cousin of this slice's audit script; the engineer should read STEP 3 to align symbol-extraction logic)
- Slice 109 (sqlc toolchain pin) — the precedent for "build a small reproducible script + wire it into CI" pattern at this repo scale

## Dependencies

- None. Pure tooling slice; no other slice needs to merge first.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT remove a dep without first appearing in the audit script's `PHANTOM` classification AND surviving a CI-green test. "I think this is unused" is not sufficient evidence.
- **P0-A2**: Does NOT remove `go.mod` deps via this audit. Go's compiler enforces direct-dep correctness already; the equivalent for go.mod drift is `go mod tidy`, which is its own discipline. The audit script SHOULD emit a "run `go mod tidy`" recommendation for go-modules ecosystem but MUST NOT edit `go.mod` directly.
- **P0-A3**: Does NOT extend scope to indirect/transitive cleanup, lockfile pruning, version unification, or licensing audit. Each of those is its own slice if needed.
- **P0-A4**: **The USED-VIA-CONFIG allowlist (AC-3) is load-bearing.** The script MUST treat config-file references as first-class usage signals. Removing eslint plugins because they're "not imported" would silently disable lint enforcement. The allowlist MUST be enumerated in the script (not a TODO), and the slice's decisions log MUST record any extension to it.
- **P0-A5**: Does NOT pick recurring-cadence option (c) (pre-commit hook that blocks commits) without documenting the friction tradeoff in the decisions log. Aggressive blocks on contributor first-PR experience are a real cost in an OSS project.
- **P0-A6**: Does NOT auto-merge the resulting PR. JUDGMENT slice → maintainer reviews the cadence-mechanism choice + the removal list before merge.

## Skill mix

- Bash/shell scripting (audit script implementation; ripgrep proficiency)
- Multi-ecosystem dependency awareness (npm vs pip vs go-modules vs uv conventions)
- GitHub Actions CI job authoring (if cadence option b is picked)
- pre-commit framework (if option c is picked — though P0-A5 raises the friction bar)
- `jq` / `awk` for tsv post-processing
- Slice 109's `Go · sqlc generate diff` informational-CI-job pattern (the closest precedent if option b)

## Notes for the implementing agent

- **Read STEP 3 of `Plans/prompts/08-dependabot-pr-review.md` first.** The symbol-extraction logic there is the spiritual cousin of this slice's audit script — same patterns for npm/pip/go-modules import detection. Reusing the logic (or factoring it out) is cleaner than re-inventing.
- **The `web/package.json` `shadcn` dep is a CLI tool** invoked by `npx shadcn add <component>` (not imported in source). The audit must classify it `USED-VIA-SCRIPT` or maintainers will think it's phantom. Verify by reading `scripts:` in `package.json` + any `justfile` recipes.
- **For Python deps in `docs-site/requirements.txt`**, the consumers are mkdocs plugins activated in `mkdocs.yml`'s `plugins:` block AND `theme.name:`. The audit's USED-VIA-CONFIG check must read mkdocs.yml's nested YAML structure, not just grep for the package name.
- **Provenance**: surfaced 2026-05-16 in `/loop dep-review` analysis of PR #154 (lucide-react phantom dep) — see `https://github.com/mgoodric/security-atlas/pull/154#issuecomment-4468964835`. The session that filed this slice also filed slice 117 (StepSecurity Harden-Runner) and slice 119 (Playwright port-3000 fix); this slice is part of the same dep-hygiene wave.
- **Cadence-option tradeoff guidance** (judgment input for AC-9 — engineer can disagree but record disagreement in the decisions log):
  - Option (a) is "ratchet doesn't tighten" — by the time the audit runs, multiple phantoms have shipped
  - Option (b) is "minimal-friction signal at the right time" — PR-time comment, contributor sees + can address before merge, never blocks
  - Option (c) is "high friction for the wrong actor" — first-time contributors fork the repo + try to commit, hit the hook, get confused; maintainers are the wrong target for the deterrent
  - Option (d) is "we won't actually do this" — manual quarterly audits don't happen in solo-maintainer projects under deadline pressure
  - The maintainer's lean is (b) and that lean tracks the slice 069 / 109 informational-CI-job pattern (signal without blocking)
- **JUDGMENT discipline:** per slice template's "Slice types" + the maintainer's note in CLAUDE.md, this is a JUDGMENT slice. The engineer makes the design call (cadence option, allowlist coverage edge cases, classification logic for edge ecosystems) themselves and records each choice in the decisions log. Do NOT block the merge on a maintainer decision for these calls.
