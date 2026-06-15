# 743 — Controls-list Playwright e2e — seed fixture + un-comment slice 448/468 AC bodies

**Cluster:** Infra / Test
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 468 (server-backed bulk-assign-owner + saved
filter-views). Captured as a follow-up per the continuous-batch policy +
CLAUDE.md e2e discipline ("if the bootstrap CANNOT seed the preconditions,
do NOT relax the spec — file a spillover slice for the seed harness").

Slice 468 landed the backend + FE for the multi-select / bulk-assign /
saved-views surface and **updated** the quarantined slice-448/468 assertion
bodies in `web/e2e/controls-list.spec.ts` to reflect the new reality (the
slice-448 bulk-assign FUTURE-STATE disclosure was replaced by a WORKING
bulk-assign trigger; the saved-views store moved from client localStorage to
the server-backed `/v1/saved-views`). But it left the bodies **commented**,
consistent with the whole `controls-list.spec.ts` file's pre-existing
slice-079/082 quarantine state: that spec is not yet wired to the
`seedFromFixture()` harness, so its preconditions are not established by the
docker-compose bootstrap, and the assertions cannot be observed passing in
CI's `Frontend · Playwright e2e` job.

This slice closes that gap and un-comments the bodies so the controls-list
spec joins the gated CI rotation.

**Preconditions the slice-448/468 assertions need (beyond the slice-006 SCF
catalog the original slice-098 bodies assume):**

- **≥3 selectable anchor rows** in the demo tenant so select-all-in-view +
  per-row toggles exercise a non-degenerate set.
- **A seeded active user** in the demo tenant (the e2e demo user already
  exists — `DEMO_USER_ID`) so the bulk-assign "assign to me" round-trip has a
  valid `owner_user_id`, and the saved-views per-(tenant, user) store has a
  real user to scope to.
- **A clean saved-views table for the demo (tenant, user)** at spec start so
  the save/load/delete/duplicate-name assertions are deterministic (the
  fixture truncates the demo user's `saved_views` rows).

**What this slice ships:**

- `fixtures/e2e/controls-list.sql` — idempotent (`ON CONFLICT DO NOTHING`)
  seed establishing ≥3 active controls in the demo tenant (varied
  `control_family` so the Family pill has options) + a reset of the demo
  user's `saved_views` + `control_owner_assignments` rows so each run starts
  clean.
- `web/e2e/seed.ts` `FixtureName` extended with `"controls-list"`.
- `web/e2e/controls-list.spec.ts` `beforeAll(seedFromFixture("controls-list"))`
  added; the slice-098 AC-1…AC-8 + slice-448 AC-1/AC-3/AC-4/AC-5 + slice-468
  AC-2 assertion bodies un-commented; the mocked `data-testid` selectors
  verified against the live impl (note the slice-468 testid change:
  `controls-bulk-assign-future` → `controls-bulk-assign-owner`, and the new
  `controls-bulk-assign-message`).
- Updated `web/e2e/README.md` Files table with the controls-list spec row.

## Acceptance criteria

- [ ] **AC-1.** `fixtures/e2e/controls-list.sql` exists, is idempotent, and is
      documented inline with a header naming what each INSERT establishes.
- [ ] **AC-2.** `seed.ts` `FixtureName` includes `"controls-list"`.
- [ ] **AC-3.** `controls-list.spec.ts` invokes `seedFromFixture("controls-list")`
      in `beforeAll`; the slice-098/448/468 AC bodies are un-commented.
- [ ] **AC-4.** The slice-468 AC-2 body drives the WORKING bulk-assign trigger
      (`controls-bulk-assign-owner`), asserts the success message
      (`controls-bulk-assign-message`), and asserts the selection clears — NOT
      the retired future-state disclosure.
- [ ] **AC-5.** The slice-448 AC-4/AC-5 saved-view bodies drive the
      server-backed store (save → appears in `<select>` → reload re-applies →
      delete) and the duplicate-name 409 surfaces the inline error.
- [ ] **AC-6.** Spec passes locally against the docker-compose self-host bundle.
- [ ] **AC-7.** Spec passes in CI against the `Frontend · Playwright e2e` job.
- [ ] **AC-8.** `web/e2e/README.md` Files table lists the spec.
- [ ] **AC-9.** CHANGELOG entry naming the un-quarantine (closes slice 468 e2e
      follow-on).

## Dependencies

- **#082** Playwright seed-data harness (merged) — extends.
- **#098** /controls list view (merged) — covers.
- **#448** bulk-ops + saved-views frontend shell (merged) — covers.
- **#468** server-backed bulk-assign + saved-views (this PR) — closes its e2e
  follow-on.

## Anti-criteria (P0 — block merge)

- **P0-743-1.** No vendor-prefixed test bearer / no real-looking secrets in the
  SQL. Use the slice-082 HMAC-hashed neutral pattern.
- **P0-743-2.** Spec MUST NOT mutate state outside the fixture's rows (no
  cross-test interference); the bulk-assign + saved-view writes target ONLY the
  demo (tenant, user) the fixture owns.
- **P0-743-3.** Does NOT relax any assertion to make it pass — if a precondition
  cannot be seeded, the assertion stays quarantined with a cited reason rather
  than weakened.

## Notes for the implementing agent

Provenance: filed 2026-06-12 from slice 468 (server-backed bulk-assign +
saved-views). The backend + FE are merged; this is purely the e2e
seed-fixture-plus-un-quarantine. Pattern is well established by the slice-164
settings fixture and the six other per-spec fixtures.
