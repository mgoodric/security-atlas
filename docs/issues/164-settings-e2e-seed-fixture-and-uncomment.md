# 164 — Settings Playwright e2e — seed fixture + un-comment AC bodies

**Cluster:** Infra / Test
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 154 (settings page audit), captured as follow-up
per continuous-batch policy.

Slice 154 expanded `web/e2e/settings.spec.ts` to declare AC-7 through
AC-10 (notifications grid coverage, time-zone select wiring, tokens
empty state, roles tail badge) but left all assertion bodies commented
behind the slice 079 / 082 quarantine pattern. The settings spec is
not yet wired to the `seedFromFixture()` harness because:

1. `web/e2e/seed.ts` `FixtureName` does not list `"settings"`.
2. There is no `fixtures/e2e/settings.sql`.

This slice closes both gaps and un-comments the assertion bodies so
the Settings spec joins the gated CI rotation.

**What this slice ships:**

- `fixtures/e2e/settings.sql` — admin user with two API key rows
  (one rotated, one fresh, varied scopes/kinds), one slice-034
  session row, preferences with at least one non-default cell, and
  a profile with a non-default time zone.
- `web/e2e/seed.ts` `FixtureName` extended with `"settings"`.
- `web/e2e/settings.spec.ts` `beforeAll(seedFromFixture("settings"))`
  added; assertion bodies un-commented; mocked `data-testid`
  selectors verified against the live impl.
- Updated `web/e2e/README.md` Files table with the settings spec row.

## Acceptance criteria

- [ ] AC-1: `fixtures/e2e/settings.sql` exists, is idempotent (`ON
CONFLICT DO NOTHING`), and the SQL is documented inline with
      a header comment naming what each INSERT establishes.
- [ ] AC-2: `seed.ts` `FixtureName` includes `"settings"`.
- [ ] AC-3: `settings.spec.ts` invokes `seedFromFixture("settings")`
      in `beforeAll`; all AC bodies un-commented.
- [ ] AC-4: Spec passes locally against the docker-compose self-host
      bundle.
- [ ] AC-5: Spec passes in CI against the `Frontend · Playwright
e2e` job (post-slice-082 un-quarantined gate).
- [ ] AC-6: `web/e2e/README.md` Files table lists the spec.
- [ ] AC-7: CHANGELOG entry: "Playwright e2e: settings page coverage
      gated on CI (#164; closes slice 154 F11)".

## Dependencies

- **#082** Playwright seed-data harness (merged) — extends.
- **#103** Settings page (merged) — covers.
- **#108** `/v1/me/*` endpoints (merged) — covers.
- **#154** Settings page audit (this PR, merged) — closes F11.

## Anti-criteria (P0 — block merge)

- **P0-164-1** No vendor-prefixed test bearer / no real-looking
  secrets in the SQL. Use the slice-082 HMAC-hashed neutral pattern.
- **P0-164-2** Spec MUST NOT mutate state outside the test fixture's
  rows (no cross-test interference).
- **P0-164-3** Plaintext-once spec assertion (slice 103 P0-A2) MUST
  pass — the issuance flow under e2e must verify the bearer
  disappears from DOM on dismiss + survives reload.

## Notes for the implementing agent

Estimated 1.5–2 hours. Pattern is well established by the six
existing per-spec fixtures.

Provenance: filed 2026-05-18 from slice 154 audit (F11).
