# 274 — Settings spec AC-9 token-row flake: deterministic-fail investigation

**Cluster:** Quality / e2e
**Estimate:** 0.5d-1d (depending on root-cause depth)
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** spillover from batch 104 (slices 229 + 268). Filed 2026-05-24.

## Narrative

Over the 2026-05-24 continuous-batch session the Playwright spec
`web/e2e/settings.spec.ts:288 › AC-9: API tokens section renders
empty-state or row table` has shifted from intermittent flake to
deterministic failure. Observed run-by-run:

- PR #587 (slice 214 sidebar counts) — failed once, passed on rerun (flake)
- PR #592 (slice 229 dashboard header) — failed once, passed on rerun (flake)
- PR #593 (slice 268 unified search) — failed 4 consecutive times,
  including after a rebase onto current `main` (deterministic on
  that branch even though `main` itself passed the same test)

The failure: `await page.getByTestId("settings-token-row").count()`
returns 0; the test asserts `> 0`. The seeded `api_keys` rows
(`55555555-5555-5555-5555-555555550001` + `0002` in
`fixtures/e2e/settings.sql`) should produce two rows in the table.

### Hypotheses to investigate

1. **Fixture-cross-contamination**: slice 213's audits-header.sql
   shares the user UUID `44444444-...0001` with settings.sql via
   `ON CONFLICT DO NOTHING`. The api_keys rows reference this user
   by `issued_by`. If the user's column values are clobbered to a
   shape that breaks `/api/admin/credentials` projection (e.g.
   missing a column the slice 192/250 `/v1/me` resolver now
   requires), the table renders the empty branch.
2. **Slice 250 (settings Profile credential-bearer) interaction**:
   the new `isCredentialBearer(profile)` predicate runs against
   `/v1/me`. If a specific shape of seeded user matches the
   predicate, the Profile section renders the credential-bearer
   variant and the Tokens section might (incorrectly) gate off.
3. **Playwright parallelism**: settings.spec.ts may run in the same
   worker as a spec that mutates the `api_keys` table (e.g.
   slice 163's rotate-twice spec at AC-11). Order-dependent state.
4. **Schema drift**: a recent migration may have added a column
   that the BFF projection now filters on, dropping the seeded
   rows from the response.

### What ships in this slice

- **Investigation**: reproduce locally (`cd web && npm run test:e2e
settings.spec.ts`); capture the actual `/api/admin/credentials`
  response and compare against the seeded rows.
- **Root cause + fix**: most likely a seed-fixture extension (add
  missing column) or a Playwright worker-isolation directive on
  settings.spec.ts. Whatever the fix, it must restore AC-9 to
  PASS on every fresh CI run.
- **Regression guard**: add a documented expectation in
  `web/e2e/README.md` that fixtures using shared UUIDs must call
  out the column set each consuming spec depends on.

## Threat model

**Verdict.** no-mitigations-needed. Pure test/fixture engineering;
no auth or data-path changes.

## Acceptance criteria

- [ ] AC-1: 5 consecutive CI runs of `settings.spec.ts` against
      a fresh DB pass AC-9 without retry.
- [ ] AC-2: root cause documented in
      `docs/audit-log/274-settings-ac9-token-row-flake-decisions.md`.
- [ ] AC-3: if the fix is a fixture extension, ANY future fixture
      that shares the same UUID must include all columns required
      by ANY consuming spec (documented in `web/e2e/README.md`).
- [ ] AC-4: CHANGELOG bullet under `### Fixed`.

## Dependencies

- **#163** (rotate-twice spec — possible parallel-state interaction)
- **#192** (/v1/me — possible projection change)
- **#250** (settings Profile credential-bearer — possible variant interaction)

## Anti-criteria (P0 — block merge)

- **P0-274-1**: does NOT disable / skip AC-9. The test must pass.
- **P0-274-2**: does NOT mask the failure with retries — the root
  cause must be fixed.

## Notes for the implementing agent

The fastest path is probably:

1. `cd web && npm run test:e2e settings.spec.ts` locally against a
   fresh docker-compose stack to reproduce.
2. Add `console.log` of the BFF response in the AC-9 test to see
   what shape comes back when rowCount==0.
3. Compare against the seeded api_keys rows in
   `fixtures/e2e/settings.sql:217-275`.
