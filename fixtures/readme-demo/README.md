# readme-demo fixtures

Deterministic, neutral seed data used by `web/scripts/capture-readme-screenshots.spec.ts`
to render the four core views (dashboard, control detail, audit workspace,
board pack preview) without spinning up Postgres + NATS + the Go platform.

## Constraints

All fixture content is neutral and reusable in public artifacts:

- **No real tenant names.** Demo tenant is `demo-tenant`; demo customer is
  `Acme Industries`; demo user is `demo-operator`.
- **No maintainer references.** No "Matt", "Goodrich", "mgoodric", real
  domains, real email addresses.
- **No vendor-prefixed tokens.** Any cred-shaped field uses
  `demo-<purpose>` (e.g., `demo-bearer-token`) — never `ghp_*`, `sk_*`,
  `eyJ*`, `AKIA*` (GitGuardian-safe even though these are test files).
- **No PII.** No real names, emails, phone numbers, addresses.

These constraints cross-reference slice 050's sanitization rules and
slice 057's P0-A2 anti-criterion.

## Files

| File                       | Used by view             |
| -------------------------- | ------------------------ |
| `dashboard-drift.json`     | `/dashboard`             |
| `dashboard-freshness.json` | `/dashboard`             |
| `dashboard-risks.json`     | `/dashboard`             |
| `dashboard-upcoming.json`  | `/dashboard`             |
| `controls-list.json`       | `/controls` + sidebar    |
| `control-detail.json`      | `/controls/[id]`         |
| `audit-period.json`        | `/audit/[controlId]`     |
| `audit-control.json`       | `/audit/[controlId]`     |
| `board-pack.json`          | `/board-packs/[id]`      |
| `anchors-list.json`        | various (sidebar counts) |
| `me.json`                  | top-bar identity         |

## Refreshing

The fixtures are static JSON, hand-curated against the API shapes in
`web/lib/api.ts`. When the UI or API contract changes, update the
relevant fixture and re-run `just refresh-screenshots`.
