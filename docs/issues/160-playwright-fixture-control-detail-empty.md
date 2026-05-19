# 160 — Add missing fixtures/e2e/control-detail-empty.sql (slice 152 leftover)

**Cluster:** Quality
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`

## Narrative

**WHY.** Slice 152 (control detail 404 D1-hybrid) shipped `web/e2e/control-detail-empty.spec.ts` which calls `seedFromFixture("control-detail-empty")` in its `beforeAll`. The `seedFromFixture` helper from slice 082's harness (`web/e2e/seed.ts`) resolves the name to `fixtures/e2e/control-detail-empty.sql` and shells out to `psql -v ON_ERROR_STOP=1 -f <path>`. The file does not exist on `main`. The harness throws:

```
Error: seed: fixture not found at /home/runner/work/security-atlas/security-atlas/fixtures/e2e/control-detail-empty.sql
```

This was latent because the `Frontend · Playwright e2e` CI job is gated by `dorny/paths-filter@v4`; every PR between slice 152's merge and slice 153's PR #330 hit the docs-only stub. Slice 153's PR was the first whose file changes (`deploy/docker/web.Dockerfile`, `web/package.json`, new `web/e2e/logo-render-production-build.spec.ts`) pushed the path filter into `code=true` and ran the real job. Two pre-existing failures surfaced; the missing fixture is one. (The other — `auth-open-redirect.spec.ts` — gets its own slice 161.)

The spec's preamble (verbatim above the `import` block) documents what the fixture must establish:

- `TEST_BEARER` carries a credential in a tenant with **zero** instantiated controls (the fresh-install state slice 152's D1-hybrid is supposed to surface)
- `BOGUS_CONTROL_ID` is a syntactically-valid UUID that won't resolve — the fixture pre-publishes `00000000-0000-0000-0000-000000000152` as the canonical value

**WHAT.** Add `fixtures/e2e/control-detail-empty.sql` matching the shape of the sibling fixtures in `fixtures/e2e/` (slice 082 pattern). For the harness-wiring scope of this slice, the fixture is essentially a no-op — the "empty state" is the absence of inserts, not a positive insertion. The fixture sets `app.current_tenant` to the harness tenant (`00000000-0000-0000-0000-00000000d3a0`, the slice-082 convention) and commits an empty transaction. This is enough for `beforeAll` to succeed and for the spec's commented assertion stubs to remain stable until per-spec un-skip lands (slice 112 — already filed `not-ready` per slice 082 decisions).

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT un-skip the spec's commented assertions. That's slice 112's job (already filed, `not-ready`, gated on slice 082's 5-clean-runs proof).
- Does NOT change the spec file itself. The fixture's contract is determined by the spec's preamble; the fixture conforms, not the other way around.
- Does NOT change `web/e2e/seed.ts` or the harness wiring. Slice 082 already owns that surface.
- Does NOT add a separate fixture for `auth-open-redirect.spec.ts` — different root cause, separate slice 161.

## Threat model

**Spoofing.** N/A — fixture is read-only test data, no auth surface.

**Tampering.** The fixture executes against a Postgres test database via `psql -v ON_ERROR_STOP=1`. Risk: a SQL injection vector in the fixture itself if it ever interpolates user input. **Mitigation:** the fixture is static SQL with hardcoded UUIDs (per slice 082 convention). No interpolation. AC-2 verifies via `grep` that the fixture has zero `\set` directives with non-literal values.

**Repudiation.** N/A — test fixture, no audit-log surface.

**Information disclosure.** The fixture must NOT contain real production data or PII. **Mitigation:** AC-3 verifies the fixture uses only the slice-082 synthetic tenant UUID (`00000000-0000-0000-0000-00000000d3a0`) and the slice-152 bogus control UUID (`00000000-0000-0000-0000-000000000152`). Both are documented neutral test values; `grep -E '[1-9a-f]{8}-[1-9a-f]{4}'` should match none of the fixture's UUIDs.

**Denial of service.** N/A — fixture is a single-transaction commit with zero inserts. No query plan blowup risk.

**Elevation of privilege.** The fixture runs via the harness's psql connection (slice 082 owns the role binding — typically `atlas_app` with RLS context set). Risk: if the fixture accidentally calls `SET ROLE postgres` or similar, it could escalate. **Mitigation:** AC-4 verifies the fixture has zero `SET ROLE` / `SET SESSION AUTHORIZATION` / `\connect` statements. The harness controls the role; the fixture stays in its lane.

**Anti-criteria added from threat model:** P0-A2 (no SQL interpolation), P0-A3 (only synthetic UUIDs), P0-A4 (no role escalation).

## Acceptance criteria

- [ ] AC-1: `fixtures/e2e/control-detail-empty.sql` exists. Header matches the sibling-fixture pattern in `fixtures/e2e/control-detail.sql` (slice + spec reference + harness contract notes).
- [ ] AC-2: Fixture is pure static SQL — zero `\set` directives with non-literal values, zero shell interpolation, zero `psql` meta-commands beyond `\set ON_ERROR_STOP on`.
- [ ] AC-3: Fixture's only UUIDs are the slice-082 synthetic tenant (`00000000-0000-0000-0000-00000000d3a0`) and the slice-152 bogus control id (`00000000-0000-0000-0000-000000000152`). No real-data UUIDs.
- [ ] AC-4: Fixture has zero `SET ROLE`, `SET SESSION AUTHORIZATION`, or `\connect` statements. Role binding stays with the harness.
- [ ] AC-5: Running the spec locally via `cd web && npx playwright test e2e/control-detail-empty.spec.ts --headed=false` against the docker-compose stack succeeds — the `beforeAll` no longer throws "seed: fixture not found".
- [ ] AC-6: `Frontend · Playwright e2e` CI job (real, not stub) passes on the PR — no more `control-detail-empty.spec.ts` failures in the run summary. (The spec's commented assertions remain commented; this slice does not un-skip them.)
- [ ] AC-7: README updated in `fixtures/e2e/README.md` — add `control-detail-empty.sql` to the per-spec fixture index.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only."** Single new file, zero edits to existing code.
- **CLAUDE.md "Never assert without verification."** AC-5 runs the spec locally; AC-6 verifies CI is green.
- **Slice 082's harness contract.** Fixture conforms to the seed-from-fixture name-resolution convention. No harness changes.
- **Slice 152's deferral.** Assertions stay commented per slice 152's deferred-to-slice-112 design.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Playwright + e2e testing
- Slice 082 (`082-playwright-seed-data-harness.md`) — the harness contract
- Slice 112 (`112-playwright-control-detail-extend-fixture.md`) — the un-skip follow-on (already filed, `not-ready`)
- Slice 152 (`152-control-detail-404.md`) — the spec author
- `web/e2e/seed.ts` — the harness loader
- `fixtures/e2e/control-detail.sql` — sibling-fixture pattern

## Dependencies

- #082 — merged. Harness must exist for the fixture to be loaded.
- #152 — merged. Spec must exist for the fixture to have a consumer.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT un-skip the spec's commented assertions. That belongs to slice 112.
- **P0-A2** (from threat model — Tampering): Does NOT use shell interpolation, `psql` variable substitution from environment, or non-literal `\set` directives. Pure static SQL only.
- **P0-A3** (from threat model — Information disclosure): Does NOT include real-data UUIDs, real tenant slugs, real user emails, or anything that grep-resembles production data. Only synthetic `00000000-...` UUIDs.
- **P0-A4** (from threat model — Elevation of privilege): Does NOT include `SET ROLE`, `SET SESSION AUTHORIZATION`, `\connect`, or any role-affecting statement.
- **P0-A5**: Does NOT modify `web/e2e/seed.ts` or any spec file. Pure fixture addition.
- **P0-A6**: Does NOT modify the `auth-open-redirect.spec.ts` failure (separate root cause; slice 161).
- **P0-A7**: Does NOT use vendor-prefixed test fixture tokens (carry-over convention).

## Skill mix

- Slice 082 harness contract (read `web/e2e/seed.ts` once)
- Postgres `\set ON_ERROR_STOP on` + `BEGIN/COMMIT` transaction pattern (one of the simplest fixtures in `fixtures/e2e/`)
- Playwright local-run verification (`cd web && npx playwright test`)
- `dorny/paths-filter@v4` triggering (verify by touching a `.go` or `.tsx` file ephemerally if needed, then reverting)

## Notes for the implementing agent

**Why this slice is 0.25d, not something bigger:** the fixture's "empty state" is the absence of inserts — the slice 152 D1-hybrid empty-state surface tests what happens when there are no controls to render. The fixture sets the tenant context, opens a transaction, commits with zero inserts, and ends. Total content is maybe 15-20 lines of SQL mostly comments.

**Suggested file body (template — adapt to repo conventions):**

```sql
-- Slice 160 — Playwright e2e seed for `web/e2e/control-detail-empty.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The control-detail-empty spec's preconditions (per its
-- preamble):
--
--   - TEST_BEARER carries a credential in a tenant with ZERO
--     instantiated controls (the slice 152 fresh-install scenario).
--   - BOGUS_CONTROL_ID is a syntactically-valid UUID that the platform
--     guarantees does not resolve in this tenant. The fixture publishes
--     `00000000-0000-0000-0000-000000000152` as the canonical value.
--
-- The "empty state" is the absence of inserts — this fixture
-- intentionally does NOT add any tenant_controls rows. The harness
-- baseline (00-seed.sql) already establishes the tenant + auth; this
-- fixture just opens + closes a transaction in that tenant's context
-- to confirm the harness wiring works.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- Intentionally empty — slice 152's empty-state spec asserts behavior
-- when the tenant has zero instantiated controls.

COMMIT;
```

**Cross-link to slice 153 reconcile:** the slice 153 batch-59 reconcile PR (#331) listed this as spillover candidate #2. Once slice 160 ships, that spillover line is resolved.

**On verifying CI passes without un-skipping slice 112:** the spec's `beforeAll` block must succeed, but the actual `test(...)` blocks are commented out. So "green" here means "harness loads fixture without error AND the (empty) test set passes" — not "the test bodies execute." If slice 112 has already merged by the time this slice opens, then AC-6 means the active assertions also pass. If slice 112 is still `not-ready` at slice-160-merge-time, AC-6 means "no new failures introduced; the spec is a successful no-op."

**Provenance:** Surfaced 2026-05-18 during slice 153 (logo standalone fix) PR session. Spillover candidate #2 in batch-59 reconcile PR #331 notes.
