# 120 — Audit and remove phantom dependencies — decisions log

Slice 120 is `Type: JUDGMENT`. This log records the build-time judgment
calls made while implementing `scripts/audit-deps.sh`, the initial
removal pass, the recurring-cadence mechanism, and the CONTRIBUTING.md
subsection.

Format: Decision · Diagnosis · Alternatives weighed · Trade-off · Revisit-trigger.

---

## D1 — Recurring-cadence mechanism (AC-9)

**Decision:** Option **(b)** — PR-comment CI check on manifest changes.
A new `Deps · phantom audit` job in `.github/workflows/ci.yml` runs
`scripts/audit-deps.sh` when any of the four manifests changes, then
posts (or updates) a sticky PR comment listing any PHANTOM
classifications. `continue-on-error: true`; not in
`.github/branch-protection.json` required-checks; informational only.

**Diagnosis:** AC-9 surfaces four options; the maintainer's lean is
(b); the slice's "Notes for the implementing agent" enumerate the
trade-offs the maintainer weighed.

**Alternatives weighed (AC-10 — all four documented):**

| Option                                                                              | Pros                                                                                                                                                                                                                                                       | Cons                                                                                                                                                                                                                                                                                    | Verdict                            |
| ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------- |
| **(a)** Periodic AFK slice (re-run every N weeks, file removals as follow-up slice) | Zero CI infrastructure; runs on the maintainer's cadence; no contributor friction                                                                                                                                                                          | Latency: by the time the audit runs, multiple phantoms have shipped; "ratchet doesn't tighten" — most prior CI-hygiene slices that committed to "re-run quarterly" silently stopped after the first quarter                                                                             | Rejected                           |
| **(b)** PR-comment CI check on manifest changes (informational, non-blocking)       | Signal at the right time (PR review); contributor sees the result before merge; never blocks; cheap to maintain (15-line workflow snippet); composes naturally with the established slice 069 / 109 informational-CI-job idiom (`sqlc-drift`, `npm-audit`) | Adds one informational job to ci.yml; another comment in the PR thread for contributors to read; trivial rebase conflict surface with PR #262 (slice 117 — currently HELD pending slices 122+123)                                                                                       | **Chosen**                         |
| **(c)** Pre-commit hook blocking commit on PHANTOM                                  | Most aggressive enforcement; PHANTOM literally cannot land on `main`                                                                                                                                                                                       | High friction for the wrong actor — first-time OSS contributors fork the repo, try to commit, hit the hook, get confused; maintainers are the wrong audience for the deterrent (they already follow the convention); P0-A5 mandates documented justification before picking this option | Rejected (P0-A5 friction tradeoff) |
| **(d)** Leave manual (script-only; maintainer runs quarterly)                       | Zero tooling overhead; lowest cognitive load                                                                                                                                                                                                               | "Manual quarterly audits don't happen in solo-maintainer projects under deadline pressure" (slice doc's exact framing) — option (d) is option (a) without even the calendar reminder; behaves the same as no recurrence at all                                                          | Rejected                           |

**Trade-off:** Option (b) costs one informational CI job (~ ten seconds of CI time per relevant PR) in exchange for a per-PR signal that phantom deps are surfacing right when they would land. The "comment-only, non-blocking" framing matches the established repo idiom: `sqlc-drift` (slice 109) and `npm-audit` (slice 089) both follow this exact pattern — `continue-on-error: true`, not in branch-protection required-checks, log+artifact for evidence. Reusing the idiom keeps the cognitive load on contributors low (it looks and behaves like the jobs they already know).

**Conflict surface with held PR #262 (slice 117):** PR #262 adds a `harden-runner` step as the first step of every job in `ci.yml`. The new `Deps · phantom audit` job introduced by slice 120 will, on rebase, need a `harden-runner` step prepended once 117 lands. That rebase is mechanical (a 4-line YAML insertion at the top of the job's `steps:` block). Slice 120's branch lands before 117 unblocks; 117's eventual rebase will pick up the new job and add the harden-runner step there in the same rebase pass that updates the other 40 jobs.

**Revisit-trigger:**

- After slice 117 (PR #262) merges + the next 2-week soak window, audit how often `Deps · phantom audit` posts a meaningful comment vs. a no-op. If the comment fires more than ~once a quarter with a real PHANTOM, the cadence is working as designed. If it never fires, then the slice 120 removal pass + steady-state contributor discipline is sufficient and option (a)/(d) would have been just as good — but the cost is so low that "always-on" is the right default.
- If contributor feedback indicates the comment is noise (e.g. it fires for known KEEP cases like `react-dom` because the rebase removed the keep-list entry), switch to option (c)+keep-list OR add a `--ignore <pkg>` flag to the script that the workflow can pass.

---

## D2 — USED-VIA-CONFIG allowlist coverage (AC-3, P0-A4 load-bearing)

**Decision:** The allowlist enumerates 16 glob patterns in
`scripts/audit-deps.sh` under `CONFIG_GLOBS`:

```
.eslintrc*               eslint.config.*
.prettierrc*             prettier.config.*
postcss.config.*         vitest.config.*
playwright.config.*      tailwind.config.*
next.config.*            tsconfig*.json
components.json          *.css
pyproject.toml           mkdocs.yml
.pre-commit-config.yaml
```

**Diagnosis:** AC-3 lists 12 entry-point file patterns; the slice's
threat-model section flags P0-A4 as load-bearing (removing eslint
plugins because they're "not imported" would silently disable lint
enforcement).

**Why each entry exists:**

- `eslint.config.*` / `.eslintrc*` / `.prettierrc*` / `prettier.config.*` — JS/TS lint+format plugins are loaded by name from the config, never `import`ed by application code
- `postcss.config.*` / `tailwind.config.*` — CSS pipeline plugins resolved by name
- `vitest.config.*` / `playwright.config.*` — test-framework presets / reporters / projects loaded by config
- `next.config.*` / `tsconfig*.json` — framework-level config; Next plugins / TS types load by name
- `components.json` — shadcn CLI config (registers icon library, color tokens, primitives source)
- `*.css` — Tailwind v4 / PostCSS / shadcn entrypoints often import packages from CSS using `@import "<pkg>"`. The real repo confirms two such cases: `web/app/globals.css` lines `@import "tw-animate-css"` and `@import "shadcn/tailwind.css"`. Without the `*.css` allowlist `tw-animate-css` would classify as PHANTOM.
- `pyproject.toml` — `[tool.*]` sections register the package as the consumer of a Python tool (`[tool.ruff]`, `[tool.hatch.build]`)
- `mkdocs.yml` — `plugins:` and `theme.name:` references; the audit's pip-docs classifier reads mkdocs.yml's nested YAML structure
- `.pre-commit-config.yaml` — pre-commit hooks reference packages by `repo:` URL and `id:` slug

**Extensions to the slice doc's enumerated list:**

- `prettier.config.*` (in addition to `.prettierrc*`) — modern Prettier deployments use a JS module file; the slice doc's list omits this variant. Added defensively.
- `components.json` — shadcn-specific; not in the slice doc's enumeration but real-world the only consumer signal for several runtime-irrelevant CLI tools.

**Allowlist extensions logged here** so future contributors don't need
to re-derive them.

**Revisit-trigger:** if a follow-on PR removes a dep that the audit
classified as PHANTOM and the build breaks in CI, the allowlist is the
first place to check. Add the missing entry, re-classify the dep,
revert the removal.

---

## D3 — `@types/<pkg>` ambient TypeScript packages (USED-VIA-CONFIG)

**Decision:** Any `@types/<pkg>` declared in `web/package.json` is
auto-classified as USED-VIA-CONFIG when the corresponding runtime
`<pkg>` is also declared (or when `<pkg>` is `node`, which is the
ambient Node environment). Orphan `@types/<X>` declarations (where
`<X>` has no runtime dep) still classify as PHANTOM.

**Diagnosis:** TypeScript's `@types/` packages are loaded by `tsc`'s
automatic node-modules `@types/` resolution — no `import` syntax
exists. The naive USED scan would mark them all PHANTOM and a removal
would silently break the build (no compile error initially because
type signatures fall back to `any` under `skipLibCheck: true`, but
type safety vanishes).

**Alternatives weighed:**

- Treat all `@types/*` as USED-VIA-CONFIG unconditionally — fails to surface the genuine "we orphaned `@types/X` after removing `X`" case
- Require `tsconfig.json`'s `types: []` array to enumerate every used `@types/X` and treat missing ones as PHANTOM — too aggressive; TypeScript's default is "include every `@types/` package in node_modules"

**Chosen heuristic:** runtime-pkg-presence is the proxy for "we
actually use this." The orphan-detection escape hatch matters
infrequently but matters absolutely when it does.

**Revisit-trigger:** if a contributor reports a real type-checking
regression after a slice 120 removal, the orphan-detection path is
suspect first — make sure the corresponding runtime pkg is still
declared.

---

## D4 — Python pip-name → import-name alias map

**Decision:** The pip-bridge classifier maintains an alias map for
common pip packages whose import name differs from the pip name:

```
compliance-trestle → trestle
grpcio             → grpc
grpcio-tools       → grpc_tools
pyyaml             → yaml
pillow             → PIL
beautifulsoup4     → bs4
pycryptodome       → Crypto
python-dateutil    → dateutil
```

**Diagnosis:** The naive PEP 8 normalization (`-` → `_`) covers the
majority of pip packages but misses ~8 well-known cases. Without the
map, those would classify as PHANTOM and be removed in error
(verified locally: `compliance-trestle` and `grpcio` both classify
PHANTOM under naive normalization despite extensive use in
`oscal-bridge/atlas_oscal_bridge/serializer.py`).

**Alternatives weighed:**

- Use `python -c "import <pkg>; print(<pkg>.__file__)"` to ask Python itself — requires the dep to be installed; brittle in CI before `uv sync`
- Parse `pyproject.toml`'s `[tool.setuptools.packages.find]` or wheel-metadata `top-level.txt` — adds an `unzip-wheel` dependency; too much for a 30-second audit
- Hard-coded alias map — explicit, fast, easy to extend; entries land in this log

**Chosen heuristic:** explicit alias map. Only 8 entries cover the
~95th-percentile case for Python's wider ecosystem; the bridge today
hits 3 of them (`compliance-trestle`, `grpcio`, `grpcio-tools`).

**Revisit-trigger:** if a new pip dep in `oscal-bridge` classifies as
PHANTOM despite being used, the alias-map is the first place to
check. Add the entry here with the import name.

---

## D5 — Initial removal pass: scope + commit shape

**Decision:** Two npm packages removed in one commit
(`deps(web): remove phantom dependencies (#120)`):

- `lucide-react@^1.16.0` (dependency) — the surfacing case from PR #154; zero source imports, no CSS import, no script invocation. `components.json` references `lucide` as `iconLibrary`, which is shadcn-CLI metadata (which icon package to install when scaffolding), not a runtime consumer
- `@radix-ui/react-slot@^1.1.2` (dependency) — declared, never imported. The repo's shadcn primitives use `@base-ui/react` instead

Three other PHANTOM classifications kept (per AC-8) with rationale:

| Pkg                   | Why keep                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `@vitest/coverage-v8` | vitest's coverage provider is peer-detected by the `vitest run --coverage` invocation in `web/package.json`'s `test:coverage` script. Removing it would make `npm run test:coverage` fail at coverage-report time. The audit script's USED-VIA-SCRIPT scan does not pick it up because vitest auto-detects providers by package presence rather than literal CLI invocation. Tracked in a follow-on: extend the audit's peer-detection knowledge of vitest's provider lookup, OR document this as a known KEEP case. Lower priority than the slice itself. |
| `react-dom`           | Required peer of Next.js 16. Next.js loads `react-dom/client` and `react-dom/server` from `node_modules` at runtime through its own bundler, no source-level import is needed in user code. Removing it would break `next build`. Documented as KEEP. (The audit script could special-case Next.js peers, but the slice's AC-3 anti-pattern P0-A3 explicitly excludes scope creep into framework-peer-awareness — that's a different problem domain.)                                                                                                      |

**Diagnosis:** AC-7 requires one commit per ecosystem (max 4); only
the npm ecosystem has actionable phantoms. go, pip-bridge, and
pip-docs all classify clean. Net commits in this PR: 1 removal commit
(npm) + the script-add + the workflow-add + CONTRIBUTING.md + this
decisions log + status-flip — all separate commits per the
"surgical fixes" discipline.

**Revisit-trigger:** if `@vitest/coverage-v8` or `react-dom` ever do
need to be removed (e.g. coverage provider switched, framework
swapped), the KEEP entries above should be removed from this log
first so the audit's PHANTOM classification matches the removal
intent.

---

## D6 — Script implementation language: bash + ripgrep + jq

**Decision:** `scripts/audit-deps.sh` is pure bash invoking `rg` (ripgrep) and `jq`. No Go binary, no Python script, no Node script.

**Diagnosis:** AC-1 allows engineer's choice; the slice doc's "bash is the lowest-friction default" framing aligns with the existing tooling pattern (`scripts/audit-rls.sh`, `scripts/install.sh`, `scripts/install_test.sh` are all bash).

**Alternatives weighed:**

- **Go binary** — would compose with the existing `cmd/scripts/coverage-gate` precedent (slice 069). But: needs `go run` invocation, JSON parsing of `package.json` by hand or via a 3rd-party lib, regex-search via `regexp` (slower than rg), and a build step. Wrong tool for ~200 lines of glue code.
- **Python script** — `uv run audit-deps.py` would land naturally next to `oscal-bridge`. But: adds a Python toolchain dependency to a CI job that otherwise needs none, slower startup than bash for a script that runs in <1s wall-clock.
- **TypeScript script** — `npx tsx audit-deps.ts` would compose with `web/scripts/capture-readme-screenshots.ts` precedent. But: tsx + node startup is ~300ms vs bash's ~10ms, and the script is mostly shell-invocation-shaped anyway.

**Chosen tool:** bash matches the precedent and is the fastest tool for the shape of the script (lots of subprocess invocations, very little in-memory data structure manipulation).

**Revisit-trigger:** if the script ever needs to maintain state across runs (e.g. for trend-tracking phantom-count over time), the language choice is worth re-opening — bash is fine for stateless one-shots, much less fine for stateful workflows.

---

## D7 — `--ecosystem` flag plus `AUDIT_DEPS_ROOT` env override

**Decision:** The script exposes a `--ecosystem <npm|go|pip-bridge|pip-docs>` flag (AC-6) and an `AUDIT_DEPS_ROOT` environment-variable override for testing.

**Diagnosis:** AC-6 mandates the ecosystem flag for scoped runs (relevant in CI where only one manifest changed in a PR). The env override is purely a testability affordance — the test harness in `scripts/audit-deps_test.sh` builds a synthetic fixture tree under `/tmp` and points the script at it.

**Trade-off:** the env override means the script's behaviour is not purely deterministic given the same git tree if `AUDIT_DEPS_ROOT` is set externally. The contributor-facing contract documented in the script's `--help` is "AUDIT_DEPS_ROOT defaults to the script's own git toplevel; explicit override is for the test harness only." The CI workflow does not set the variable.

**Revisit-trigger:** if a use case emerges for auditing a non-toplevel subtree (e.g. a vendored submodule with its own `package.json`), the override is already in place — document the contract change here.
