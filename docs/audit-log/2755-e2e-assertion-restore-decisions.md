# 2755 — Restore assertions in the 163 assertion-free Playwright tests: decisions log

Issue: mgoodric/security-atlas#2755 (follow-up from #2711 / PR #2750).

## Detection-tier classification

- **detection_tier_actual:** contract. The AST guard
  `web/lib/e2e-tests-assert.test.ts` (#2750) found 163 tests in 13 specs
  whose bodies never assert. Restoring them against a live stack surfaced
  three product bugs on `/evidence` (#2771) and one on `/calendar` (#2772),
  which had shipped while those tests reported green.
- **detection_tier_target:** playwright. The specs existed; their
  assertions sat in comments behind the slice-082 quarantine.

## Decisions

### D1 — every list spec seeds a tenant of its own

Counts, filter results and empty states read rows that other specs add to
the demo tenant, and fixtures are additive across a run. Each spec whose
assertions depend on what a tenant holds seeds one or more tenants of its
own in `fixtures/e2e/<spec>.sql` and signs in with `signInToTenant` from
the new `web/e2e/tenant-session.ts`. That replaces the per-spec mint
helper copied into about 20 specs; existing specs are not migrated here.

UUIDs carry `2755a`–`2755d` at the start of their last group, so fixtures
written in parallel never share an id.

### D2 — assert what ships now, not what the slice doc described

Where the product kept the behaviour under a new testid, heading or copy
(`Calendar` not `Compliance calendar`, the KPI strip's `control-state-strip`,
the History and Framework map tabs on control detail), the test asserts the
shipped surface. Where a later ticket replaced the behaviour on purpose,
the test asserts the replacement and its title says so:

| Spec                     | Old test                                         | Now asserts                                                         |
| ------------------------ | ------------------------------------------------ | ------------------------------------------------------------------- |
| `risks-list.spec.ts`     | AC-185-1..3 (rows inert, hierarchy link, banner) | one test: row click opens `/risks/[id]` (#2132), link + banner gone |
| `evidence-list.spec.ts`  | AC-7 inline drawer                               | row click opens `/evidence/<id>` with the full hash (#2137)         |
| `policies-list.spec.ts`  | AC-3 ack-rate em-dash; new-policy disclosure     | ack-rate bar for published rows; `New policy` is a live link        |
| `controls-list.spec.ts`  | slice 225 New control disclosure                 | no disabled button; create path is `Import controls` (#1785)        |
| `metrics-dashboard.spec` | `audit_readiness` / `per_framework_coverage`     | the catalog's `audit_readiness_score` / `framework_coverage_pct`    |

### D3 — `test.fixme` only with a linked issue

Seven tests stay `test.fixme`, each with its reason and issue beside it:

| Spec                    | Test                       | Why                                                | Issue |
| ----------------------- | -------------------------- | -------------------------------------------------- | ----- |
| `evidence-list.spec.ts` | AC-3 control pill          | option values are anchor ids, filter wants control | #2771 |
| `evidence-list.spec.ts` | slice 234 Scope pill       | pill offers scope cells, filter reads `scope_id`   | #2771 |
| `evidence-list.spec.ts` | slice 234 Source pill      | wire `source` is filled from `provenance`          | #2771 |
| `calendar.spec.ts`      | AC-183-1 / AC-183-2        | events stay unlinked though detail pages exist     | #2772 |
| `admin-bootstrap.spec`  | SSO save / reload / resave | e2e stack sets no `ATLAS_OIDC_SECRET_KEY` (503)    | #2773 |
| `risks-list.spec.ts`    | AC-8 sidebar entry         | `/risks/hierarchy` is back in the nav; intent?     | #2774 |

### D4 — the guard drops KNOWN_EMPTY and closes three blind spots

With every count at zero the ratchet is gone: the guard now expects no
assertion-free test anywhere. From the #2750 review it also:

- scans `e2e/` and `e2e-audit/` recursively;
- follows functions declared in the same spec file, and counts an imported
  `expect` alias (`expect as anonExpect`, which `security-headers.spec.ts`
  uses) as an assertion;
- checks aliased and extended test functions (`test as authed`,
  `base.extend(...)`) and titles that are not string literals.

An imported helper still counts only if its name starts with `expect`; that
rule is in `web/e2e/README.md`.

### D5 — fixtures that two workers apply at once

`fullyParallel` runs a spec's `beforeAll` once per worker, so two workers
apply the same fixture concurrently. Delete-then-insert fixtures collided on
primary keys. They now either take `pg_advisory_xact_lock` first
(`exceptions-list.sql`) or upsert at `now()` instead of deleting
(`control-detail.sql`, `controls-list.sql`, `metrics-dashboard.sql`).

### D6 — one shape for the shared demo user row

`controls-list.sql` (from main), `audits-header.sql` and `owner-picker.sql`
each insert the demo user `44444444-…-0001` with `ON CONFLICT DO NOTHING`,
so the first fixture applied sets its name and time zone. The controls-list
row said "Controls-list E2E Operator" with no time zone. A full local run
with four workers seeded it first, and audits-header AC-4 and settings AC-8
failed. CI's two workers happened to seed audits-header first. The
controls-list row now matches the other two column for column.
