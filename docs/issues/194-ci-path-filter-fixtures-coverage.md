# 194 — CI path filter should include `fixtures/**` so fixture-only PRs trigger Playwright e2e (slice 193 spillover)

**Cluster:** Infra/CI
**Estimate:** 0.25d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 193 (fixture-only fix for the dashboard.spec.ts AC-5 upcoming-row failure).

The `.github/workflows/ci.yml` `changes` job uses `dorny/paths-filter` to short-circuit the expensive jobs (Go integration, Playwright e2e, etc.) on docs-only PRs. The `code` filter currently lists every code-path glob — `**/*.go`, `web/**`, `internal/**`, `migrations/**`, `connectors/**`, etc. — but it does NOT list `fixtures/**`.

Net effect: a PR that touches ONLY `fixtures/e2e/*.sql` (a Playwright e2e fixture) is classified as `code: false` and the `Frontend · Playwright e2e` job short-circuits to its 8-second stub sibling. The fixture's correctness is never exercised against the real spec.

Slice 193 hit this directly. The fix touched `fixtures/e2e/dashboard.sql` + decisions log + `_STATUS.md` — none of those globs matched the code filter. Engineer worked around it by adding a slice-193 preamble note to `web/e2e/dashboard.spec.ts` (which IS in the filter), but that's a workaround, not the right fix.

## Acceptance criteria

- **AC-1.** `.github/workflows/ci.yml` `changes` filter adds `fixtures/**` to the `code:` glob list.
- **AC-2.** A PR touching only `fixtures/e2e/<spec>.sql` produces `changes.code == true`, fires the real Playwright job (not the stub).
- **AC-3.** Existing behavior preserved: docs-only PRs (`docs/**`, `Plans/**`, `*.md`) still short-circuit to stubs.
- **AC-4.** Decisions log at `docs/audit-log/194-ci-path-filter-fixtures-decisions.md` captures the change and the slice-193 surface.

## Threat model

Pure CI configuration. No security surface beyond "the right gate fires on the right diff". **Verdict: clean.**

## Anti-criteria (P0 — block merge)

- **P0-194-1.** Does NOT add `fixtures/**` paths to OTHER, unrelated filters (e.g., the `schemas` filter).
- **P0-194-2.** Does NOT relax existing filters' precision (no broadening `**/*.sql` etc.).
- **P0-194-3.** Does NOT change the stub-sibling pattern.

## Skill mix

- `simplify`
- `ship-gate`

## Notes for the implementing agent

This is a ~2-line patch. Add `'fixtures/**'` to the `code:` glob list and verify CI on a fixture-touch PR.
