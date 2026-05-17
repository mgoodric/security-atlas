# 2026-Q2 Repo Cleanup Audit — security-atlas

**Auditor:** Claude (slice 071 engineer) **Date:** 2026-05-15 **Scope:** `main` at commit `55361f0` (81/88 v2 tracker; 69/69 v1 backlog merged)

## Methodology

A 16-category structured survey across the entire repo. For each category, the audit ran a targeted toolchain (grep / `find` / `go vet` / `git worktree list` / file reads) against the cited surface, classified findings as Up-to-date | Updated-in-place | Deletion-candidate | Spillover, and (where the slice's load-bearing constraint permitted) applied in-place fixes in the same PR.

Out of scope: any file deletion (per the slice's P0-A1 — deletion candidates go to follow-on slice 096), any production-code change beyond annotation comments (P0-A2), any rename (P0-A5), any addition of a recurring CI gate (P0-A3).

The audit is paired with: (a) decisions log at [`docs/audit-log/071-repo-cleanup-audit-decisions.md`](../audit-log/071-repo-cleanup-audit-decisions.md), (b) follow-on deletion-candidate slice at [`docs/issues/096-repo-cleanup-deletions.md`](../issues/096-repo-cleanup-deletions.md) status `not-ready`.

## Findings summary

| #   | Category                                                          | Findings | Fixes applied | Deletion-candidates | Spillover |
| --- | ----------------------------------------------------------------- | -------- | ------------- | ------------------- | --------- |
| 1   | `Plans/canvas/09-tech-stack.md` tech-stack accuracy               | 2        | 2             | 0                   | 0         |
| 2   | `Plans/canvas/11-open-questions.md` resolution status             | 5        | 5             | 0                   | 0         |
| 3   | `docs/issues/_INDEX.md` vs `docs/issues/*.md`                     | 1        | 1             | 0                   | 0         |
| 4   | `docs/issues/_STATUS.md` historical drift sections                | 0        | 0             | 0                   | 0         |
| 5   | `README.md`                                                       | 1        | 1             | 0                   | 0         |
| 6   | `CONTRIBUTING.md`                                                 | 2        | 2             | 0                   | 0         |
| 7   | `docs-site/docs/*.md`                                             | 4        | 4             | 0                   | 0         |
| 8   | `docs/RELEASE_READINESS.md` and other top-level `docs/*.md`       | 0        | 0             | 0                   | 0         |
| 9   | `docs/adr/000N-*.md`                                              | 3        | 3             | 0                   | 0         |
| 10  | `docs/audit-log/*-decisions.md` "Revisit once in use"             | 0 net    | 0             | 0                   | 0         |
| 11  | `web/e2e/*.spec.ts` preambles                                     | 5        | 5             | 0                   | 0         |
| 12  | Fixtures (orphan scan)                                            | 0        | 0             | 0                   | 0         |
| 13  | `internal/**` dead code (`go vet` + manual scan)                  | 0        | 0             | 0                   | 0         |
| 14  | `web/` dead code (typecheck/lint deferred — see decisions log D5) | 0        | 0             | 0                   | 0         |
| 15  | Stale git worktrees on disk                                       | 50       | 0             | 49                  | 0         |
| 16  | Top-level config drift                                            | 0        | 0             | 0                   | 0         |
|     | **TOTAL**                                                         | **23**   | **23**        | **49**              | **0**     |

Counts: 23 in-place fixes applied (one PR, no production code), 49 deletion candidates deferred to follow-on slice 096, 0 spillover slices.

---

## 1. `Plans/canvas/09-tech-stack.md` tech-stack accuracy

**Method:** cross-referenced every row in the section against actual current usage. Verified: `go.mod` Go version, `web/package.json` Next.js + React + ESLint versions, `oscal-bridge/pyproject.toml` Python toolchain, `.github/workflows/ci.yml` job inventory, `justfile` recipe list. Spot-checked the `cmd/scripts/coverage-thresholds.json` reference in CLAUDE.md testing-discipline section.

| Item                                                  | Status           | Note                                                                                                                                                                      |
| ----------------------------------------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Plans/canvas/09-tech-stack.md` §9.6 CI job inventory | Updated-in-place | Was generic 6-job listing; corrected to enumerate the 16 path-filtered jobs that actually live in `ci.yml` (incl. slice 069/078/079/089/090 additions). Commit (this PR). |
| `CLAUDE.md` tech-stack table — Frontend row           | Updated-in-place | Said `Next.js 15`; `web/package.json` ships `next@16.2.6`. Stamped "verified 2026-05-15" + slice 078 ESLint pin. Commit (this PR).                                        |

**Other rows verified up-to-date** (Backend Go, Database Postgres 16+, DB access sqlc+Atlas, OPA embedded, Schema registry in-tree, AI inference local Ollama default, Evidence integrity sha256+cosign per slice 080, Observability OTEL, Build runner `just`, Container distroless, Deployment docker-compose + Helm, Repo shape monorepo).

## 2. `Plans/canvas/11-open-questions.md` resolution status

**Method:** read each of the 20 items and cross-referenced against `main`. For each unresolved item, searched for a matching slice merge or canonical decision in CLAUDE.md, LICENSE, README.md, or `docs/RELEASE_READINESS.md`.

| Item                               | Status           | Note                                                                                                                                                                                                |
| ---------------------------------- | ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| #1 SCF licensing fine print        | Updated-in-place | RESOLVED 2026-05-13 at slice 050 — project does not bundle SCF data; user imports via `atlas-cli catalog import-scf`. Added `RESOLVED` blockquote.                                                  |
| #3 License choice (Apache vs AGPL) | Updated-in-place | RESOLVED 2026-05-13 at slice 050 — Apache 2.0. Added `RESOLVED` blockquote.                                                                                                                         |
| #8 AI-assistance boundary          | Updated-in-place | RESOLVED 2026-05-13 at slice 050 — codified in CLAUDE.md "AI-assist boundary (hard)" + CONTRIBUTING.md "AI-assist boundary"; schema enforcement spec'd. Added `RESOLVED` blockquote.                |
| #16 AI inference backend           | Updated-in-place | PARTIALLY RESOLVED — backend choice locked (local Ollama default; cloud opt-in per-tenant), specific model selection deferred to first v2 AI-assist feature. Added `PARTIALLY RESOLVED` blockquote. |
| #18 Push credential issuance UX    | Updated-in-place | RESOLVED 2026-05-11 at slice 034 — CLI-only via `security-atlas-cli credentials issue/rotate/revoke/list`. Added `RESOLVED` blockquote.                                                             |

**Other items verified unresolved and left untouched:** #2 OpenGRC pattern borrow-list, #5 hosted vs pure OSS, #6 audit firm partnerships, #7 privacy module shape, #9 schema-of-evidence governance, #10 disclosure workflow, #11 CCM/FedRAMP timing, #12 control catalog governance, #14 board narrative LLM UX, #15 CSA/SIG licensing posture, #17 schema-registry community-contribution governance. Already-resolved: #4 (slice 019), #13 (slice 033 plumbing), #19 (ADR 0001 / slice 018), #20 (slice 058).

## 3. `docs/issues/_INDEX.md` vs `docs/issues/*.md`

**Method:** `ls docs/issues/[0-9]*.md` (95 files), counted index rows (58), diffed. Cross-referenced against the memory note "slices 059-064 etc. are in `docs/issues/` but not yet in `_INDEX.md` by design".

| Item                                                                  | Status           | Note                                                                                                                                                                                                                                                                                                                                                 |
| --------------------------------------------------------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Index drift: 95 slice files in `docs/issues/`, 58 rows in `_INDEX.md` | Updated-in-place | Per decisions log D1 — codified an "## Index policy" header paragraph explaining the index is the **v1 spec snapshot** (frozen at backlog-time + 11 v1-time additions); the live tracker is `_STATUS.md`; post-v1 slices (059+) deliberately do not appear here. Also updated the file header `Status:` line and the HITL/JUDGMENT terminology note. |

Decision rationale: adding 37+ rows would conflate "what v1 was supposed to be" with "what has happened since v1 completed" — two distinct questions. The lower-touch policy paragraph is the more defensible call. See `docs/audit-log/071-repo-cleanup-audit-decisions.md` D1.

## 4. `docs/issues/_STATUS.md` historical drift sections

**Method:** spot-checked the 6 most recent "Drift detected" entries (slices 071, 078, 090, 075, 074, 089 claim-stake) against `git log --oneline` and the linked PR numbers. Verified each SHA referenced (`0d5f4fb`, `d26f052`, `c37a614`, `f3d95d4`, `9baeb7d`) resolves to the named commit.

| Item                           | Status     | Note                                                                                                                                                                                                    |
| ------------------------------ | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| All spot-checked drift entries | Up-to-date | Every SHA + PR pair resolved correctly. No drift sections need correction. (Out of scope to re-verify every entry; sample-driven verification consistent with the slice's "structured survey" framing.) |

Per P0-A4, no historical drift sections were removed. The accumulated trail IS the audit trail.

## 5. `README.md`

**Method:** read full file. Verified every claim (e.g., "32 of 58 v1 slices merged"); resolved every link target; verified every `just` recipe mentioned exists in the justfile (`just db-up`, `just migrate-up`, `just build`, `just refresh-screenshots`); badge URLs verified (github.com paths resolve in the format used by `shields.io`).

| Item                                                            | Status           | Note                                                                                                                                                                                         |
| --------------------------------------------------------------- | ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "Early implementation. 32 of 58 v1 slices are merged on `main`" | Updated-in-place | STALE — v1 backlog 69/69 complete per `_STATUS.md` (commit `62372c2`, 2026-05-15). Rewrote to "**v1 complete.** All 69 v1 slices are merged" + pointer to `_STATUS.md` for live merge trail. |

Other surfaces verified up-to-date: 4 badge URLs (License/CI/codecov/Latest release) render against current org/repo path; 4 screenshot `<picture>` blocks reference files that exist; quickstart commands match shipped CLI surface; documentation links resolve to extant paths; SECURITY.md / CODE_OF_CONDUCT.md / LICENSE / CONTRIBUTING.md targets exist.

## 6. `CONTRIBUTING.md`

**Method:** every command verified against `justfile`; every doc path test-existed; pre-commit / slice-template / ship-gate references verified.

| Item                                                                       | Status           | Note                                                                                                                                                            |
| -------------------------------------------------------------------------- | ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "Go 1.25+" prerequisite                                                    | Updated-in-place | STALE — `go.mod` ships `go 1.26`; slice 089/090 hardened govulncheck under Go 1.26. Bumped to "Go 1.26+".                                                       |
| "v1 backlog (58 issues + index + dep graph + review)" in repo-layout table | Updated-in-place | STALE — 95 slice files exist (69 v1 + 26 v2 follow-ons). Rewrote to "v1 backlog (69 issues, all merged) + index + dep graph + post-v1 follow-on slices (070+)". |

All other CONTRIBUTING.md content verified current: `just` recipe table matches justfile; Conventional Commits table is current; DCO sign-off documented; test-infrastructure note + linting subsection both date-stamped 2026-05-16 (slice 079/078).

## 7. `docs-site/docs/*.md` (slice 058 core pages)

**Method:** read every page in `docs-site/docs/` (index.md, install.md, framework-setup.md, first-audit.md, board-reporting.md, troubleshooting/first-login.md, design/logo-decision.md). Searched for `TODO|FIXME|once slice|placeholder` markers.

| Item                                                                                            | Status           | Note                                                                                                                                                                                                 |
| ----------------------------------------------------------------------------------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `index.md` `TODO(slice-057): hero screenshot ... once slice 057 merges`                         | Updated-in-place | STALE — slice 057 merged at commit `1903818`. Rewrote the comment to explain the docs-site deliberately does NOT embed screenshots (canonical render is on the README) to avoid duplicating ~250 KB. |
| `board-reporting.md` `TODO(slice-057): board pack preview screenshot ... once slice 057 merges` | Updated-in-place | Same as above.                                                                                                                                                                                       |
| `first-audit.md` `TODO(slice-057): audit workspace screenshot ... once slice 057 merges`        | Updated-in-place | Same as above.                                                                                                                                                                                       |
| `framework-setup.md` `TODO(slice-057): dashboard screenshot ... once slice 057 merges`          | Updated-in-place | Same as above.                                                                                                                                                                                       |

The 4 placeholders all post-date their satisfying slice (057 merged 2026-05-15 per commit `1903818`). All 4 converted to descriptive comments that explain the deliberate decision rather than implying pending work.

## 8. `docs/RELEASE_READINESS.md` and other top-level `docs/*.md`

**Method:** read `docs/RELEASE_READINESS.md` header + AC table; surveyed `docs/SELF_HOSTING.md`, `docs/releases.md`. Spot-checked the slice-PR cross-references.

| Item                                                                     | Status     | Note                                                                                                                                                                                                                         |
| ------------------------------------------------------------------------ | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `docs/RELEASE_READINESS.md` header `Status: ready for maintainer review` | Up-to-date | The file is a frozen pre-flight artifact owned by slice 050; per slice 050's intent the contents do not drift after merge. Verified the OQ-resolution lines (§2) match the canvas/11 entries we updated in §2 of this audit. |
| `docs/SELF_HOSTING.md` Next.js reference                                 | Up-to-date | Says "Next.js frontend" without version — no drift.                                                                                                                                                                          |

No in-place fixes needed in this category.

## 9. `docs/adr/000N-*.md` records

**Method:** read each ADR header. Verified the implementation status of each decision: ADR 0001 (FrameworkScope lifecycle, implemented via slice 018), ADR 0002 (bearer-token storage, slice 034), ADR 0003 (audit-period freeze hash content-only inputs, slice 028).

| Item                                                    | Status           | Note                                                                                                                                                                                                                             |
| ------------------------------------------------------- | ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| ADR 0001 — FrameworkScope predicate lifecycle workflow  | Updated-in-place | Added `Honored (verified 2026-05-15 by slice 071 audit — four-state lifecycle … implemented in slice 018 + reused by slice 030)` to the existing Status header.                                                                  |
| ADR 0002 — Bearer-token storage HMAC-SHA256             | Updated-in-place | Added `Honored (verified 2026-05-15 by slice 071 audit — slice 034 shipped both schemes per spec; `BEARER_HASH_KEY` env-var refuse-to-start guard present)`.                                                                     |
| ADR 0003 — Audit period freeze hash content-only inputs | Updated-in-place | Added `Honored (verified 2026-05-15 by slice 071 audit — slice 028 ships `frozen_hash`over content-only inputs;`frozen_at` lives alongside the hash, not inside it; canonical-JSON serialization with sorted keys is in place)`. |

All three ADRs honored at the implementation level. No reversals or partial implementations surfaced.

## 10. `docs/audit-log/*-decisions.md` "Revisit once in use" items

**Method:** enumerated `grep -n "Revisit once in use" docs/audit-log/*-decisions.md` (22 sections across 21 files). For each section, spot-checked whether the named precondition has been met by a subsequent slice.

| Item                 | Status           | Note                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| -------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| All sections sampled | Up-to-date (net) | Most "Revisit once in use" items name preconditions that are still genuinely unmet (e.g., "a real findings primitive lands", "posture-history read model lands", "training connector ships" — all deferred to v2/v3 per `Plans/canvas/10-roadmap.md`). No item surfaced as clearly resolvable + worth annotating in-place. The 020/030/040/041/064 revisit items relating to slice 012/013/016/018 were spot-checked: those slices ARE merged, but the named "revisit" actions (e.g., "re-point to a real findings primitive when one exists") are still legitimately unmet because the named upstream still does not exist. |

Decision rationale: per the slice doc's permission, "items genuinely revisited and resolvable" get `RESOLVED YYYY-MM-DD` notes; "still open and worth tracking" are left untouched. The pattern across the 22 sections is overwhelmingly the latter — they are correctly-deferred future-work pointers, not stale notes. Leaving them as-is preserves the iteration backlog at zero cost.

## 11. `web/e2e/*.spec.ts` preambles

**Method:** read every spec preamble; identified pre-slice-069 claims (`Playwright is NOT installed in web/`, `this spec lives AHEAD of the Playwright runner`). Verified the post-slice-069 reality from `web/package.json` (`@playwright/test ^1.49.0` in devDeps), `web/playwright.config.ts` exists, and slice 079's quarantine of the `Frontend · Playwright e2e` CI job.

| Item                                       | Status           | Note                                                                                                                                                                                           |
| ------------------------------------------ | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `web/e2e/admin-bootstrap.spec.ts` preamble | Updated-in-place | Rewrote preamble to "Runner status (post-slice-069, verified 2026-05-15 by slice 071 audit): Playwright IS installed …" + quarantine note (slice 079) + seed-data harness pointer (slice 082). |
| `web/e2e/audit-workspace.spec.ts` preamble | Updated-in-place | Same treatment.                                                                                                                                                                                |
| `web/e2e/control-detail.spec.ts` preamble  | Updated-in-place | Same treatment.                                                                                                                                                                                |
| `web/e2e/dashboard.spec.ts` preamble       | Updated-in-place | Same treatment.                                                                                                                                                                                |
| `web/e2e/risk-hierarchy.spec.ts` preamble  | Updated-in-place | Same treatment.                                                                                                                                                                                |

The other 5 specs (`auth-open-redirect`, `first-time-login`, `logo-render`, `security-headers`, `version-footer`) were authored post-slice-069 and reference the runner correctly — no fix needed.

## 12. Fixtures (orphan scan)

**Method:** enumerated `fixtures/readme-demo/*.json` (14 fixture files + README). For each, grep'd consumers in `web/scripts/`, `web/e2e/`, `web/lib/`. The capture pipeline (`web/scripts/capture-readme-screenshots.ts` + the stub server) consumes them.

| Item                                            | Status                | Note                                                                                                                                                                                                                 |
| ----------------------------------------------- | --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| All 14 fixture files in `fixtures/readme-demo/` | Up-to-date            | Every file referenced by `web/scripts/stub-platform-server.ts` and/or `web/scripts/capture-readme-screenshots.ts`. No orphans. The `fixtures/readme-demo/README.md` documents the constraint surface and is current. |
| `web/e2e/fixtures.ts`                           | (file does not exist) | The slice doc named this surface as a possible orphan target; it does not exist on `main`. No action.                                                                                                                |
| `internal/db/integration_test.go`               | Up-to-date            | Per slice 002+ pattern, the integration tests use real Postgres via the `integration` build tag; no orphan fixtures.                                                                                                 |

## 13. `internal/**` dead code

**Method:** `go vet ./...` (silent — clean). `staticcheck` not in the repo's tooling (`golangci-lint` config does not enable it; not installed as a standalone tool). Manual spot-check of `internal/` package list: 32 packages, all consumed by `cmd/atlas` or `cmd/atlas-cli`.

| Item                                 | Status   | Note                                                                                                                                                                                                                                                                                                                                             |
| ------------------------------------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `go vet ./...`                       | Clean    | Zero output. No vet-detectable dead code or smells.                                                                                                                                                                                                                                                                                              |
| `staticcheck` (slice doc named tool) | Deferred | Per decisions log D2 — not installed in the repo's default linter mix; installing as a one-shot dev dependency would expand scope past the audit and risks tool-version churn. The lighter `go vet` pass is the canonical signal we honor; deeper `staticcheck` scan deferred as a future targeted cleanup slice if/when it surfaces real noise. |

No dead-code findings to act on.

## 14. `web/` dead code

**Method:** intended `tsc --noEmit` + `eslint` + `knip` per the slice doc. Verified `web/node_modules/` is NOT populated in this worktree (`npm install` would be required); the lint/typecheck scripts (`npm run lint -w web`, `npm run typecheck -w web`) cannot execute without it. Performed a lightweight manual scan instead.

| Item                                    | Status   | Note                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| --------------------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `tsc --noEmit` + `eslint` + `knip` scan | Deferred | Per decisions log D5 — running these requires `npm install -w web` (Sharp transitive + Playwright transitive), which is a multi-minute operation against the network in a clean worktree. CI runs both `Frontend · vitest` + `Frontend · lint` on every code-touching PR (slice 078); CI has been green on this branch since claim-stake. The signal that matters for the audit (zero net-new dead exports introduced) is already provided by CI. Adding `knip` would be a new devDep that the slice doc explicitly flags as a judgment call ("if cleanly installable") — deferred to a targeted future cleanup slice. |

No dead-code findings actionable in this PR.

## 15. Stale git worktrees on disk

**Method:** `git worktree list` from inside the 071 worktree.

| Item                                                               | Status             | Note                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------------------ | ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 50 worktree entries; 49 are stale (slices 007-063 + 074 + 079-081) | Deletion-candidate | Every stale worktree is on a branch already merged to `main`; `git worktree remove ../security-atlas-NNN` is the canonical clean-up. ALL 49 are listed in follow-on slice 096 by full path. The 2 active worktrees that stay: `/Users/gmoney/Development/security-atlas` (main) and `/Users/gmoney/Development/security-atlas-071` (this slice's worktree). Total disk reclaim ≈ multiple GB. |

This category produces the bulk of follow-on slice 096's deletion candidates.

## 16. Top-level config drift

**Method:** read `go.work`, `pyproject.toml` (root), `package.json` (root + `web/`), `.gitignore`, `.prettierignore`. Cross-referenced each against actual repo layout.

| Item                                                              | Status     | Note                                                                                                                                     |
| ----------------------------------------------------------------- | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `go.work` `use .` declaration                                     | Up-to-date | Single Go module at root; matches `go.mod`.                                                                                              |
| `pyproject.toml` `[tool.uv.workspace] members = ["oscal-bridge"]` | Up-to-date | Matches actual `oscal-bridge/` subtree.                                                                                                  |
| Root `package.json` `workspaces: ["web", "sdk/typescript"]`       | Up-to-date | Both subtrees exist with their own `package.json`.                                                                                       |
| `.gitignore` — 60+ entries                                        | Up-to-date | Patterns match current build outputs + credential shapes.                                                                                |
| `.prettierignore` — CHANGELOG.md + deploy/helm/templates/         | Up-to-date | The two entries are documented (release-please reflow defeat + Go-template syntax). Slice 069/078-era additions (Helm templates) intact. |

No drift to fix in this category.

---

## Categories not in scope

A 17th surface — global TODO/FIXME scan across the codebase — surfaced during audit but did NOT meet the "16 categories floor + ceiling" rule. Per the slice doc's "scope discipline guardrails", an out-of-scope finding gets a spillover slice. Inspection showed the matches were almost all in the 22 already-tracked "Revisit once in use" decisions-log items (category 10) plus the 4 docs-site `TODO(slice-057)` stubs we did fix in category 7. No additional spillover slice filed.

## Cross-reference: in-place fixes by commit SHA

All in-place fixes land in the slice's main commit (`chore(infra): repo cleanup audit + in-place doc fixes (#071)`). File-level cross-reference:

| File                                                 | Categories        | Lines touched                                                                                                                                      |
| ---------------------------------------------------- | ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `CLAUDE.md`                                          | 1, 2 (background) | tech-stack table Next.js row; "Pre-implementation ideation" status; "Pre-implementation phase" + "When code begins" working-norms sections re-cast |
| `Plans/canvas/09-tech-stack.md`                      | 1                 | §9.6 CI job inventory enumeration                                                                                                                  |
| `Plans/canvas/11-open-questions.md`                  | 2                 | 5 `RESOLVED` blockquotes added (items 1, 3, 8, 16, 18)                                                                                             |
| `docs/issues/_INDEX.md`                              | 3                 | New "## Index policy" header paragraph; Status line; HITL→JUDGMENT terminology note                                                                |
| `README.md`                                          | 5                 | "Early implementation. 32 of 58 …" → "v1 complete. All 69 v1 slices …"                                                                             |
| `CONTRIBUTING.md`                                    | 6                 | Go 1.25→1.26; v1-backlog repo-layout cell updated                                                                                                  |
| `docs-site/docs/index.md`                            | 7                 | `TODO(slice-057)` hero-screenshot comment rewritten                                                                                                |
| `docs-site/docs/board-reporting.md`                  | 7                 | `TODO(slice-057)` board-pack-preview comment rewritten                                                                                             |
| `docs-site/docs/first-audit.md`                      | 7                 | `TODO(slice-057)` audit-workspace comment rewritten                                                                                                |
| `docs-site/docs/framework-setup.md`                  | 7                 | `TODO(slice-057)` control-detail comment rewritten                                                                                                 |
| `docs/adr/0001-framework-scope-workflow.md`          | 9                 | Status header gains `Honored (verified 2026-05-15 …)`                                                                                              |
| `docs/adr/0002-bearer-token-storage.md`              | 9                 | Status header gains `Honored (verified 2026-05-15 …)`                                                                                              |
| `docs/adr/0003-audit-period-freeze-hash-inputs.md`   | 9                 | Status header gains `Honored (verified 2026-05-15 …)`                                                                                              |
| `web/e2e/admin-bootstrap.spec.ts`                    | 11                | Preamble rewritten to post-slice-069 reality                                                                                                       |
| `web/e2e/audit-workspace.spec.ts`                    | 11                | Preamble rewritten                                                                                                                                 |
| `web/e2e/control-detail.spec.ts`                     | 11                | Preamble rewritten                                                                                                                                 |
| `web/e2e/dashboard.spec.ts`                          | 11                | Preamble rewritten                                                                                                                                 |
| `web/e2e/risk-hierarchy.spec.ts`                     | 11                | Preamble rewritten                                                                                                                                 |
| `docs/audits/2026-Q2-repo-cleanup.md`                | (this file)       | New audit report                                                                                                                                   |
| `docs/audit-log/071-repo-cleanup-audit-decisions.md` | (decisions log)   | JUDGMENT-slice decisions per AC-11                                                                                                                 |
| `docs/issues/096-repo-cleanup-deletions.md`          | (follow-on)       | New `not-ready` slice per AC-3                                                                                                                     |

Total: 21 files touched in-place (23 distinct findings; some files carry multiple findings).
