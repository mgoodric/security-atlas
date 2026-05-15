# 082 — Playwright e2e seed-data harness

**Cluster:** Infra
**Estimate:** 2-3d
**Type:** AFK
**Status:** not-ready

## Narrative

Authored by slice 079 (which quarantined the `Frontend · Playwright e2e`
job via `continue-on-error: true`). This slice removes the quarantine
by shipping the seed-data harness that the five un-shimmed specs need:
`admin-bootstrap`, `audit-workspace`, `control-detail`, `dashboard`,
`risk-hierarchy`. The two route-mocked specs (`first-time-login`,
`version-footer`) need nothing.

Slice doc 079 + its decisions log at `docs/audit-log/079-quarantine-playwright-e2e-decisions.md`
carry the full reasoning for the quarantine and the per-spec
preconditions.

**Not-ready until staffed.** No specific blocking dependency — flip to
`ready` when a maintainer chooses to take this on.

## Acceptance criteria

- [ ] AC-1: Harness function `seedFromFixture(name)` in `web/e2e/seed.ts`
      populates Postgres + MinIO + NATS to a named spec's preconditions
      (seeded test user + tenant data + per-spec entities — risks,
      controls, evidence, exceptions per the spec headers).
- [ ] AC-2: Each of the five un-shimmed specs invokes
      `seedFromFixture()` in `test.beforeAll()` before any assertion.
- [ ] AC-3: `web/e2e/fixtures.ts` extended with typed accessors for
      seeded entities so specs reference them by symbolic name.
- [ ] AC-4: `.github/workflows/ci.yml` `frontend-playwright` job —
      remove the `continue-on-error: true` line + the slice-079 comment
      block. The job again fails the PR on red.
- [ ] AC-5: Re-evaluate whether to promote the job to
      `.github/branch-protection.json` required-checks. Decision in
      the slice's decisions log.

## Dependencies

- **079** (quarantine — landed) — established the line this slice
  removes
- **069** (verification suite — landed) — original wiring

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT use real customer data to seed fixtures (canvas
  AI-assist boundary applies to test data too).
- **P0-A2**: Does NOT silence the flip-back step — the
  `continue-on-error` line MUST be removed by this slice.
- **P0-A3**: Does NOT introduce realistic-looking credential strings.
  Follow slice 069's neutral `test-bearer-e2e` / `test-*@example.com`
  pattern.

## Notes

- Fixtures already exist under `fixtures/walkthroughs/` and
  `fixtures/readme-demo/` — most of this slice is wiring, not authoring.
- Harness must be idempotent (clear-and-reapply OK).
- Run specs individually as `seedFromFixture()` calls land; do not
  batch debugging across all five at once.
