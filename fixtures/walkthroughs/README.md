# walkthroughs fixtures

Deterministic, neutral seed data used by the five executable onboarding
walkthroughs under `docs/walkthroughs/`. Applied by the
`just walkthroughs-refresh` recipe before each `uvx showboat` run.

## Distinct from slice 027

This fixture set is for the **PAI Walkthrough skill** (showboat-generated
onboarding docs, slice 070). It is unrelated to slice 027's
`internal/audit/walkthrough` package — the latter records an auditor's
evidence capture against controls. The two concepts share a word and
nothing else; see each walkthrough doc's header for the full
disambiguation.

## Constraints

All fixture content is neutral and reusable in public artifacts (same
constraints as `fixtures/readme-demo/`, cross-references slice 050
sanitization rules and slice 057 P0-A2):

- **No real tenant names.** Demo tenant is `demo-tenant`; demo customer
  is `Acme Industries`; demo user is `demo-operator`.
- **No maintainer references.** No "Matt", "Goodrich", "mgoodric", real
  domains, real email addresses.
- **No vendor-prefixed tokens.** Any cred-shaped field uses
  `demo-<purpose>` (e.g., `demo-bearer-token`) — never `ghp_*`, `sk_*`,
  `eyJ*`, `AKIA*` (GitGuardian-safe even in test files).
- **No PII.** No real names, emails, phone numbers, addresses.
- **Deterministic IDs.** All UUIDs are hard-coded so re-runs produce
  byte-identical output (modulo the showboat header timestamp + UUID).

## Files

| File                      | Used by walkthrough                               |
| ------------------------- | ------------------------------------------------- |
| `00-seed.sql`             | All — base tenant, scope, framework, control rows |
| `evaluation-pipeline.sql` | `evaluation-pipeline.md`                          |
| `audit-period.sql`        | `audit-period-freezing.md`                        |
| `rls-isolation.sql`       | `rls-tenant-isolation.md`                         |
| `schema-registry.sql`     | `schema-registry-seed-and-validate.md`            |
| `oscal-export.sql`        | `oscal-ssp-export.md`                             |

## How they're applied

The `walkthroughs-refresh` justfile recipe (slice 070 AC-4):

1. Stands up a fresh Postgres on a dedicated port via `just self-host-up`
   (slice 037 docker-compose bundle).
2. Waits for the `atlas-bootstrap` container to finish (creates roles,
   runs migrations).
3. Applies `00-seed.sql` once, then each per-walkthrough SQL in the order
   the recipe runs them.
4. Invokes `uvx showboat init/exec` against each walkthrough's
   bash blocks.

The fixtures are deliberately minimal — just enough seed data for each
walkthrough's narrative to traverse a believable scenario. They are not
a full demo dataset (that's `fixtures/readme-demo/` + future demo-data
slices).

## Refreshing

When a walkthrough's narrative changes — or when the underlying surface
materially drifts — re-run `just walkthroughs-refresh`. See
`CONTRIBUTING.md` "Refreshing walkthroughs" for the operator workflow.
