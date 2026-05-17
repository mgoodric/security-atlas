# e2e fixtures

Per-spec SQL seed files invoked by `web/e2e/seed.ts` (`seedFromFixture()`)
before each of the five un-shimmed Playwright e2e specs runs. Authored
under slice 082 — the harness slice that un-quarantined the
`Frontend · Playwright e2e` CI job after slice 079's pause.

## Distinct from sibling fixtures

| Set                      | Audience                                                                |
| ------------------------ | ----------------------------------------------------------------------- |
| `fixtures/walkthroughs/` | PAI walkthroughs (showboat-generated onboarding docs, slice 070)        |
| `fixtures/readme-demo/`  | README screenshot capture (`web/scripts/capture-readme-screenshots.ts`) |
| `fixtures/e2e/` (this)   | Playwright e2e specs (slice 082)                                        |

The three sets share the same neutrality and idempotency constraints
(below) but exist as separate trees so a refresh-screenshots run cannot
contaminate a Playwright run, and vice versa.

## Constraints

All fixture content is neutral and reusable in public artifacts (same
constraints as `fixtures/walkthroughs/` + `fixtures/readme-demo/`,
cross-references slice 050 sanitization rules and slice 057 P0-A2):

- **No real tenant names.** Demo tenant is `demo-tenant`; demo customer
  is `Acme Industries`; demo user is `demo-operator`.
- **No maintainer references.** No "Matt", "Goodrich", "mgoodric", real
  domains, real email addresses.
- **No vendor-prefixed tokens.** Any cred-shaped field uses
  `test-<purpose>` (e.g., `test-bearer-e2e`) — never `ghp_*`, `sk_*`,
  `eyJ*`, `AKIA*` (GitGuardian-safe even in test files).
- **No PII.** No real names, emails, phone numbers, addresses.
- **Deterministic IDs.** Hard-coded UUIDs across all files so a spec
  can reference rows by symbolic name via `web/e2e/fixtures.ts`.

## Files

| File                  | Spec                              |
| --------------------- | --------------------------------- |
| `dashboard.sql`       | `web/e2e/dashboard.spec.ts`       |
| `control-detail.sql`  | `web/e2e/control-detail.spec.ts`  |
| `audit-workspace.sql` | `web/e2e/audit-workspace.spec.ts` |
| `risk-hierarchy.sql`  | `web/e2e/risk-hierarchy.spec.ts`  |
| `admin-bootstrap.sql` | `web/e2e/admin-bootstrap.spec.ts` |

Each file builds on `fixtures/walkthroughs/00-seed.sql` (the harness
applies that file first, then the per-spec file). All inserts use
`ON CONFLICT DO NOTHING` so the harness is safely idempotent across
re-runs in the same CI job.

## How they're applied

`web/e2e/seed.ts` exports `seedFromFixture(name)`. The function:

1. Resolves `DATABASE_URL` from env (CI sets it; local devs export it).
2. Spawns `psql` with `ON_ERROR_STOP=1` to apply
   `fixtures/walkthroughs/00-seed.sql` (base tenant + control + scope).
3. Spawns `psql` to apply `fixtures/e2e/<name>.sql` (per-spec rows).
4. Inserts an `api_keys` row whose `token_hash` matches
   `HMAC-SHA256("test-bearer-e2e", BEARER_HASH_KEY)`, admin=true,
   tenant=demo-tenant. The platform's bearer middleware then accepts
   the `test-bearer-e2e` cookie value the Playwright fixture sets.

The harness assumes `psql` is on PATH (true on the GitHub runner
ubuntu-latest image and in any reasonable dev environment).
