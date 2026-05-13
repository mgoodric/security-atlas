# 061 — CI path-based filtering (skip expensive jobs for docs-only changes)

**Cluster:** CI / developer experience
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Every `.md`-only PR currently runs the full CI matrix — Go build+test, Go integration (Postgres RLS), Go lint, Frontend install+build, Python ruff, Proto lint, CodeQL (Go + JS), pre-commit. ~6 minutes of runners + ~10 GitHub Actions billable minutes for a 1-line `_STATUS.md` reconcile. With status PRs landing every parallel batch (1–2 per day at current velocity), this is the single biggest avoidable cost in the runner budget — and the longest wait between "edit docs" and "merge unblocked."

The fix is GitHub's path filter, but the obvious solution (`paths-ignore:` at workflow level) **breaks branch protection**: required status checks register as "Expected — waiting for status to be reported" and the merge button stays disabled forever. The robust pattern is `dorny/paths-filter` _inside_ each workflow: the workflow always starts (so the required-check name posts to GitHub) and a `changes` job uses the path filter to set a `code` output. Downstream jobs gate on `if: needs.changes.outputs.code == 'true'`. Docs-only PRs see the required check resolve in ~10s with a no-op pass.

The slice delivers value because every status / docs / planning PR (which is a large fraction of merge volume) skips the expensive jobs without weakening the merge gate — and the same required-check names continue to satisfy `.github/branch-protection.json`.

## Acceptance criteria

- [ ] AC-1: A `changes` job runs first in every workflow that has expensive downstream steps. It uses `dorny/paths-filter@v3` to produce a `code` output (boolean string). Filter list lives in one place — either inline per workflow with a shared filter definition, or in `.github/path-filters.yml` referenced via `filters-from-source-file`.
- [ ] AC-2: Path filter classifies as `code: true` any change under: `**/*.go`, `**/*.ts`, `**/*.tsx`, `**/*.js`, `**/*.py`, `go.mod`, `go.sum`, `package*.json`, `pnpm-lock.yaml`, `migrations/**`, `sql/**`, `proto/**`, `policies/**`, `schemas/**`, `connectors/**`, `web/**`, `internal/**`, `cmd/**`, `pkg/**`, `oscal-bridge/**`, `Dockerfile*`, `**/Dockerfile`, `docker-compose*.yml`, `.github/workflows/**`, `justfile`, `.golangci.yml`, `pyproject.toml`, `uv.lock`, `atlas.hcl`, `.pre-commit-config.yaml`.
- [ ] AC-3: Anything NOT in that list is `code: false`. The big ones: `*.md`, `Plans/**`, `docs/**` (except generated proto docs — those live under `proto/`), `LICENSE`, `CHANGELOG.md`, `.gitignore`, `.editorconfig`, image assets under `docs/images/` if any.
- [ ] AC-4: Each expensive job (`Go · build + test`, `Go · integration (Postgres RLS)`, `Go · lint`, `Frontend · install + build`, `Python · ruff`, `Proto · lint + generate diff`) gains `if: needs.changes.outputs.code == 'true'`. A companion `*-stub` job with the **same final job name** (via `name:` override on a docs-only echo step) runs when `code != 'true'` and posts pass — so branch-protection check names continue to resolve.
- [ ] AC-5: `pre-commit · all hooks` runs **always** (it's fast — ~30s — and catches markdown formatting nits that prettier auto-fixes; we want it on docs PRs).
- [ ] AC-6: `GitGuardian Security Checks` and `CodeQL` (both languages) run **always** — security scans should never be conditionally skipped, even on docs PRs.
- [ ] AC-7: Integration test: open a draft PR that touches only `docs/issues/_STATUS.md` and confirm: expensive jobs report pass within 30 seconds, security/lint jobs run as normal, total billable-minutes for the PR < 2 (down from current ~10).
- [ ] AC-8: Documentation: a short `docs/ci/PATH_FILTERING.md` explaining the pattern, the gotcha (why we can't use `paths-ignore:`), how to add a new "always-runs" workflow, and how the stub-job name-matching trick keeps branch-protection happy.
- [ ] AC-9: `Plans/canvas/09-tech-stack.md` updated with a 2-line note under CI/CD: `dorny/paths-filter@v3` is the gate; security + secret-scan jobs always run.

## Constitutional invariants honored

- **No security/secret-scan bypass**: AC-6 keeps CodeQL + GitGuardian unconditional. Path filtering is a cost optimization for build/test/lint, not a security exemption.
- **Required-check parity**: AC-4's stub-job pattern preserves the branch-protection contract. Required check names continue to register pass on every PR — no "waiting for status" deadlock.
- **Pre-commit unchanged**: AC-5 keeps the auto-formatter on docs PRs so prettier nits don't slip through.

## Canvas references

- `CLAUDE.md` (CI/CD: GitHub Actions · branch protection)
- `Plans/canvas/09-tech-stack.md` (CI/CD section — needs the path-filter note)
- `.github/branch-protection.json` (required check names — stub jobs MUST register the same names)

## Dependencies

- **None.** Pure CI yaml + a docs page. No DB, no Go code, no migrations.

## Anti-criteria (P0 — block merge)

- Does NOT use `paths-ignore:` at workflow `on:` level. That pattern breaks branch protection (required checks never report). See slice narrative.
- Does NOT skip CodeQL, GitGuardian, or pre-commit on docs PRs. Security scans + auto-format are always-on.
- Does NOT rename any check appearing in `.github/branch-protection.json`. Stub jobs MUST use the same `name:` so required-check resolution stays intact.
- Does NOT introduce a separate workflow file per stub. The stub job is in the SAME workflow as the real job, gated by `if:`, so GitHub treats them as one named check.

## Verification

Open a draft PR with a 1-line edit to `docs/issues/_STATUS.md`. Expected outcome:

| Check                              | Behavior  | Time  |
| ---------------------------------- | --------- | ----- |
| `Go · build + test`                | stub pass | < 30s |
| `Go · integration (Postgres RLS)`  | stub pass | < 30s |
| `Go · lint`                        | stub pass | < 30s |
| `Frontend · install + build`       | stub pass | < 30s |
| `Python · ruff`                    | stub pass | < 30s |
| `Proto · lint + generate diff`     | stub pass | < 30s |
| `pre-commit · all hooks`           | full run  | ~30s  |
| `GitGuardian Security Checks`      | full run  | ~15s  |
| `Analyze (go)` / `Analyze (js-ts)` | full run  | ~3–5m |

Total wall-clock to "all green": ~5 min (CodeQL bound) instead of ~6 min, but **billable runner minutes drop from ~10 to ~2** because the expensive jobs short-circuit.

Compare against PR #49 (the batch 14 reconcile, .md-only): all 10 checks ran full, ~6 min wall-clock, ~9 billable minutes. After this slice, the same PR would consume ~2 billable minutes.

## Skill mix (3–5)

- GitHub Actions YAML + `if:` job gating
- `dorny/paths-filter@v3` filter expression authoring
- Branch-protection required-check matching (the stub-name trick)
- CI cost analysis (billable vs wall-clock)
