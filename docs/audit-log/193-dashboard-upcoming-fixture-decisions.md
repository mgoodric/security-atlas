# Slice 193 — dashboard.spec AC-5 upcoming-row fix (decisions log)

**Slice:** 193 · **Type:** JUDGMENT · **Cluster:** Quality
**Branch:** `quality/193-dashboard-upcoming-fixture`
**Spillover from:** slice 111 (un-skip dashboard.spec assertions)

---

## D-0: Failure shape recap

Slice 111 un-skipped 11 assertion bodies in `web/e2e/dashboard.spec.ts` that
had been commented since slice 082. 10/11 went green on the first post-rebase
CI run; one failed:

```
e2e/dashboard.spec.ts:120:7 › dashboard view › AC-5: upcoming panel binds
to /v1/upcoming unified rollup (slice 157)
  Error: expect(locator).toBeVisible() failed
  Expected: visible
  - Expect "toBeVisible" with timeout 5000ms
  > 137 | await expect(page.getByTestId("upcoming-row").first()).toBeVisible();
```

Two hypotheses were possible:

- **H1 — Fixture gap.** The seed in `fixtures/e2e/dashboard.sql` doesn't
  produce rows that `/v1/upcoming` surfaces as upcoming.
- **H2 — Testid drift.** The dashboard renders a different `data-testid`
  than `"upcoming-row"` after slice 157's refactor.

---

## D-1: Verdict — H1 (fixture gap). H2 ruled out.

### Why H2 is wrong

`web/components/dashboard/upcoming-panel.tsx:98` literally renders
`data-testid="upcoming-row"` on each `<li>` of the upcoming list. The
testid the spec asserts against is exactly what the component emits. No
drift.

### Why H1 is the cause

The `/v1/upcoming` rollup is implemented in `internal/db/queries/dashboard.sql`
as `ListUpcomingItems`. The exception branch (line 231-239) filters:

```sql
FROM exceptions e
WHERE e.tenant_id = $1
  AND e.status = 'active'
  ...
```

The pre-slice-193 fixture seeded the exception with `status = 'approved'`
(NOT `'active'`). The exception_status_chk CHECK constraint allows both
(`'requested', 'approved', 'denied', 'active', 'expired'`), but the rollup
ONLY surfaces `'active'`. So the row was inserted successfully and was
silently invisible to the endpoint.

Cross-checks that confirm `'active'` is the right seed value, not a
historical artifact:

- `internal/api/dashboard/integration_test.go:258-268` —
  `seedException()` comment: _"status='active' makes it appear..."_,
  and the test inserts with `'active'`.
- `fixtures/walkthroughs/00-seed.sql:108` — the canonical demo seed uses
  `'active'`.
- `fixtures/walkthroughs/rls-isolation.sql:49` — `'active'`.
- `internal/db/queries/exceptions.sql:43,53,127` — every read query the
  exception flows through filters on `status = 'active'`, including the
  slice-040 `ListExpiringExceptions` that the upcoming panel originally
  bound to. The pre-slice-193 fixture wouldn't have satisfied the
  slice-040 binding EITHER; the bug existed from the day slice 082
  shipped the fixture, but never failed because assertions were
  commented out until slice 111.

The slice 082 fixture's `'approved'` was a typo/oversight that survived
review only because the test surface couldn't catch it.

---

## D-2: ON CONFLICT (id) DO UPDATE (not DO NOTHING)

The other five fixture INSERTs in `dashboard.sql` use `ON CONFLICT DO
NOTHING`, which is fine for rows whose initial state is correct: the
first run inserts the right row; subsequent runs skip. But the exception
row was wrong, and a stale `'approved'` row would survive a DO NOTHING
re-seed on a developer's shared local Postgres. That defeats the fix's
guarantee.

**Precedent:** `fixtures/e2e/settings.sql` slice 168 fix — the
`user_notification_preferences` row uses `ON CONFLICT (...) DO UPDATE
SET enabled = EXCLUDED.enabled` for exactly the same reason (a stale
`enabled=true` from a prior run would have broken the seed contract).
See settings.sql lines 108-125 for the explanation.

We update three columns on conflict: `status`, `expires_at`,
`activated_by`, `activated_at`. The first two are the load-bearing
ones the rollup query depends on; the latter two are needed because
the SoD CHECK constraint and the new active-state narrative ride on
them.

CI is always a fresh Postgres so DO NOTHING and DO UPDATE behave
identically there. The DO UPDATE branch only matters for developers
re-running locally against a stateful DB.

---

## D-3: Why also add activated_by / activated_at

A row with `status='active'` should logically have been activated by
someone. Pre-slice-193 the row had `status='approved'` so omitting
`activated_*` was consistent. After slice 193, the row is `active` —
so we set:

- `activated_by = 'demo-approver@example.invalid'` (DISTINCT from
  `requested_by = 'demo-operator@example.invalid'`)
- `activated_at = now() - INTERVAL '58 days'` (after the
  `approved_at = now() - INTERVAL '59 days'`)

Constraint compliance:

- `exceptions_sod` requires `approved_by <> requested_by` (already true)
  and `denied_by <> requested_by` (denied is NULL).
- No constraint explicitly forbids `activated_by = requested_by` at the
  DB level, but the application enforces SoD on activation too (every
  state-changing path through the application). Setting it to
  demo-approver keeps the seed self-consistent with the production
  semantics.

`exceptions_max_365d` is satisfied (`expires_at` = now+14d,
`requested_at` = now-60d, total delta = 74d < 365d).

---

## D-4: Anti-criteria check

P0-193-1 (no relaxation): not violated — the assertion is unchanged.
P0-193-2 (no comment-out): not violated — the spec is unmodified.
P0-193-3 (no settings.spec): not violated — only `fixtures/e2e/dashboard.sql`
and `docs/audit-log/193-...md` touched plus the `_STATUS.md` flip in
the post-PR-open commit.

---

## D-5: Why not also `expires_at` semantics

The upcoming rollup query for exceptions has NO 30-day window filter at
the SQL layer — the keyset cursor starts at `-infinity` so every active
exception with `expires_at > -infinity` qualifies. The "next 30 days"
framing is panel copy + the panel's intent, but the API surface returns
ALL active rows sorted ascending by `expires_at`. So our `expires_at =
now() + 14d` would qualify regardless of any window — and the panel's
slice-157 unified rollup also surfaces policy_ack / vendor_review /
audit_period rows without a window filter. No additional change needed.

---

## D-6: Spillover check — any OTHER dashboard.spec failures?

The slice 111 CI report shows 10/11 assertions passed; only AC-5 failed.
No additional spillovers needed.

The `settings.spec.ts` AC-5/AC-7/AC-9 failures are explicitly out of
scope per anti-criterion P0-193-3 and the slice spec.

---

## Files changed

1. `fixtures/e2e/dashboard.sql` — `status='approved'` → `'active'`;
   `ON CONFLICT DO NOTHING` → `ON CONFLICT (id) DO UPDATE`; added
   `activated_by` + `activated_at`; updated file header comment.
2. `docs/audit-log/193-dashboard-upcoming-fixture-decisions.md` — this
   file.
3. `docs/issues/_STATUS.md` — row 193 added in the status-flip commit.

---

## Verification

- Local DB-level contract test: ephemeral Postgres → migrations → base
  - dashboard seed → run `ListUpcomingItems` query → row visible with
    `category='exception'`. PASSES.
- CI `Frontend · Playwright e2e` job on the slice 193 PR — the gate.

---

**Time spent (engineer wall-clock):** ~25 min from spec-read to
decisions-log committed.
