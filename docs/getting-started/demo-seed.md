# Demo seed: populate a tenant for screenshots + live demos

This page documents `atlas-cli demo seed` — the opt-in command that
loads one polished demo dataset suitable for showing security-atlas
publicly. The dataset covers every product primitive (controls, risks,
evidence, policies, audit periods, walkthroughs, exceptions, vendors,
board reports, audit log) and produces a meaningful render across every
backend-backed UI surface.

The command is **opt-in only**. A fresh `docker compose up` does NOT
receive demo data. A production install does NOT receive demo data
unless the operator explicitly runs the command.

## Where the demo data goes

Seeding never turns an install multi-tenant on its own (#1765).

| Install                                      | Command                                       | Result                                                                                           |
| -------------------------------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Single tenant, only the stock bootstrap seed | `atlas-cli demo seed`                         | Loads into your organisation. Every added row is recorded; `demo teardown` removes exactly them. |
| Single tenant holding real data              | `atlas-cli demo seed`                         | Refused. Demo and real data are never mixed.                                                     |
| Any, with `ATLAS_ALLOW_MULTI_TENANT=true`    | `atlas-cli demo seed --tenant-slug=demo-acme` | Creates a separate demo tenant (the original behaviour).                                         |
| Has a tenant, no multi-tenant opt-in         | `atlas-cli demo seed --tenant-slug=...`       | Refused: a second tenant would make the install multi-tenant.                                    |
| Several tenants                              | `atlas-cli demo seed`                         | Refused: pass `--tenant-slug` (with the opt-in).                                                 |

"Only the stock bootstrap seed" means no evidence, no risks, no
tenant-private framework versions, and no controls or policies other than
the stock ones: the SOC 2 bundles `bootstrap.sh` uploads from
`controls/soc2/`, and the policies `atlas-cli policies seed-stock` creates.
The bootstrap user, scope dimension and scope cell are stock too.

If your organisation already holds real data, either opt in to multi-tenant
(`ATLAS_ALLOW_MULTI_TENANT=true`, one-way) and seed a separate demo tenant,
or try the demo on a separate install.

## When to use it

- You're recording a product walkthrough or live demo.
- You're taking screenshots for a blog post / pitch deck.
- You're developing a new UI surface and want a realistic dataset to
  iterate against without writing per-spec test fixtures.
- An auditor wants to see what the platform looks like populated, so
  you spin up a throwaway demo tenant for them to explore.

## When NOT to use it

- **A production install.** The seeder writes ~600 rows; you do not
  want any of those rows in production.
- **A pre-existing customer tenant.** The seeder refuses to write into
  a tenant that holds real data: in place, anything beyond the stock
  bootstrap seed; by slug, any tenant it did not create.
- **A long-lived demo environment.** The seeder generates a fresh
  password per invocation that prints once and is not recoverable.
  For long-lived deployments, rotate the password to one managed by
  your password manager via the standard `/v1/admin/users` surface.

## Prerequisites

- `atlas-cli` binary built from this repo (`go build -o atlas-cli ./cmd/atlas-cli/`).
- BYPASSRLS DSN available — typically the same `DATABASE_URL` the
  docker-compose stack hands the atlas-bootstrap container. The seeder
  works before any tenant context exists (it may create the demo
  tenant) so it needs the BYPASSRLS pool, not the application pool.
- The `ATLAS_ENABLE_DEMO_SEED=true` env var set — without it, the
  command refuses to run.

## Invocation

Into your organisation, on a fresh single-tenant install:

```bash
ATLAS_ENABLE_DEMO_SEED=true \
DATABASE_URL='postgres://atlas_migrate:...@localhost:5432/security_atlas?sslmode=disable' \
  atlas-cli demo seed
```

Into a separate demo tenant, on a multi-tenant install:

```bash
ATLAS_ENABLE_DEMO_SEED=true ATLAS_ALLOW_MULTI_TENANT=true \
DATABASE_URL='postgres://atlas_migrate:...@localhost:5432/security_atlas?sslmode=disable' \
  atlas-cli demo seed --tenant-slug=demo-acme
```

The command prints to stdout exactly once (shown for `--tenant-slug`):

```
=== Slice 205 demo seed complete ===
  tenant_slug : demo-acme
  tenant_id   : <UUID>
  admin email : admin@demo.example
  admin pass  : <one-time password>   <-- printed ONCE; capture now

Row counts:
  controls         : 50
  risks            : 20
  evidence_records : 200
  policies         : 5
  vendors          : 10
  audit_periods    : 3 (1 frozen)
  populations      : 3
  samples          : 3
  walkthroughs     : 5
  exceptions       : 10
  board_briefs     : 2
  board_packs      : 1
  framework_scopes : 3
  audit_log rows   : 51
  evidence_kinds   : 12 distinct kinds
```

**Capture the password.** It is never logged, never written to disk,
and not re-printable on subsequent invocations. If you lose it, rotate
via `/v1/admin/users` (slice 142 super_admin management surface).

## Flags

| Flag             | Default         | Notes                                                                                                                                                                                                         |
| ---------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--tenant-slug`  | (none)          | Omit to seed your organisation in place. Set to create a separate demo tenant (needs `ATLAS_ALLOW_MULTI_TENANT=true` if a tenant exists). Lower-case alphanumeric + hyphen, 1-63 chars; use a `demo-` prefix. |
| `--scale`        | `1.0`           | Row-count multiplier. `0.5` halves every per-primitive floor (still at least 1 row per primitive); `2.0` doubles them. Useful for stress-testing the UI or for compact demos.                                 |
| `--database-url` | `$DATABASE_URL` | Override the BYPASSRLS DSN. Convenient when you have multiple Atlas deployments.                                                                                                                              |

## Idempotency

Re-running against an already-seeded tenant is a no-op:

```bash
$ atlas-cli demo seed --tenant-slug=demo-acme
demo seed: tenant "demo-acme" already seeded (id=<UUID>); no changes made. Rotate the password via /v1/admin/users if needed.
```

If you want fresh demo state, use `demo teardown` first (see below).

## Teardown

When the demo is done:

```bash
ATLAS_ENABLE_DEMO_SEED=true \
DATABASE_URL='...' \
  atlas-cli demo teardown                          # your organisation, seeded in place
  atlas-cli demo teardown --tenant-slug=demo-acme  # a separate demo tenant
```

**Seeded in place:** teardown deletes exactly the rows the seed recorded
(in `demo_seed_rows`) and keeps the tenant, so outside its audit logs your
organisation is left as it was before the seed. The audit logs keep a trace:
the seed's own audit rows (all tagged `demo_seed_v`, see the filter below)
stay, and teardown adds a `demo_seed_teardown` row to `me_audit_log` with who
ran it and how many rows it removed from each table. Rows you add later that hang off a demo row follow
the schema's own delete rule: those tied by `ON DELETE CASCADE` (an owner
assigned to a demo control, say) go with it; those tied by `ON DELETE
RESTRICT` (an exception filed against a demo control) make teardown refuse
and name the table, so you can decide. Derived evaluation rows the post-seed
evaluation pass wrote for your stock controls are left in place; the
evaluator recomputes them on its next run.

**Separate demo tenant:** teardown drops the tenant + every row anchored to
it.

The teardown command refuses to operate on a tenant that does not carry
the slice-205 forensic mark (i.e., a tenant the seeder did NOT create).
A typo'd `--tenant-slug` cannot accidentally erase a real tenant.

Before deleting, the teardown writes one `super_admin_audit_log` +
one `me_audit_log` row with action `demo_seed_teardown`. Your forensic
trail retains the teardown event even though the demo rows themselves
are about to vanish.

## Forensic filter convention

Every audit-log row written by the demo seeder carries
`payload_json -> demo_seed_v = "205"` (the slice version stamp). If you
need to filter demo activity out of a forensic query:

```sql
-- Operator-driven (real) activity only
SELECT * FROM me_audit_log
WHERE tenant_id = $1
  AND NOT (after ? 'demo_seed_v');

-- Demo-seed activity only
SELECT * FROM me_audit_log
WHERE tenant_id = $1
  AND (after ? 'demo_seed_v');
```

`board_briefs.content` and `board_packs.content` likewise carry a
`demo_seed_v` key.

## What the demo dataset covers

The seeder writes:

| Primitive                                     | Rows (scale=1.0) | Notes                                                                                                                                                                                                                                        |
| --------------------------------------------- | ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `controls`                                    | 50               | Spread across 19 control families, mixed implementation types.                                                                                                                                                                               |
| `risks`                                       | 20               | All `treatment='mitigate'`. Each linked to a control via `risk_control_links`.                                                                                                                                                               |
| `evidence_records`                            | 200              | Spread across the prior 12 months (~30% within 7 days, ~40% within 30 days, ~20% 30-90 days, ~10% > 90 days). Drives the slice-016 freshness/drift dashboard surface.                                                                        |
| `policies`                                    | 5                | Standard categories — Information Security, Acceptable Use, Access Control, Incident Response, Vendor Risk Management. All `status='published'`.                                                                                             |
| `vendors`                                     | 10               | Mixed criticality, mixed DPA status, mixed review cadence.                                                                                                                                                                                   |
| `audit_periods`                               | 3                | One `frozen=true` with frozen sample population (~5 evidence pinned). Two `open`. Demonstrates the audit-period freezing primitive (canvas invariant #10).                                                                                   |
| `populations` + `samples` + `sample_evidence` | 3 + 3 + ~15      | One per audit period.                                                                                                                                                                                                                        |
| `walkthroughs`                                | 5                | All `status='finalized'` with sha256 canonical_hash.                                                                                                                                                                                         |
| `exceptions`                                  | 10               | Mixed lifecycle states (requested / approved / active / expired / denied).                                                                                                                                                                   |
| `board_briefs` + `board_packs`                | 2 + 1            | Pre-rendered narrative + structured content.                                                                                                                                                                                                 |
| `framework_scopes`                            | 3                | SOC 2, ISO 27001, NIST CSF. Scope 0 = `activated` (the partial UNIQUE index permits exactly one activated per framework_version); scopes 1-2 = `draft`.                                                                                      |
| `me_audit_log`                                | ~50              | Realistic actions (profile.update / preferences.update / session.revoke) spread across the prior 90 days.                                                                                                                                    |
| `evidence_kind` coverage                      | 12               | `osquery.host_posture`, `github.{repo_protection,audit_event,scim_user}`, `okta.{app_assignment,mfa_policy,user_lifecycle}`, `aws.s3.bucket_encryption_state`, `access_review.completion`, `sast.scan_result`, `manual.{attestation,upload}` |

## Refusal cases

The seeder errors (non-zero exit) when:

| Cause                                                           | Error message                                                                                                                        |
| --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `ATLAS_ENABLE_DEMO_SEED` is unset                               | `ATLAS_ENABLE_DEMO_SEED=true is required to run demo seed (P0-A1 — opt-in only). See docs/getting-started/demo-seed.md`              |
| `--database-url` and `DATABASE_URL` both unset                  | `--database-url or DATABASE_URL is required (BYPASSRLS DSN for the demo seed)`                                                       |
| No `--tenant-slug`, tenant holds real data                      | `refusing to seed: tenant "..." holds organisation data beyond the stock bootstrap seed (...)`, with the two ways forward            |
| No `--tenant-slug`, several tenants                             | `demo seed: this is a multi-tenant install, so demo data lives in its own tenant; pass --tenant-slug ...`                            |
| No `--tenant-slug`, no tenant yet                               | `demo seed: no tenant exists yet; run the install bootstrap first, or pass --tenant-slug ...`                                        |
| New `--tenant-slug`, another tenant exists, no opt-in           | `refusing to create demo tenant "...": it would make this install multi-tenant ...`                                                  |
| Slug has uppercase / leading hyphen / disallowed chars          | `demoseed: --tenant-slug must start with [a-z0-9]` or `demoseed: --tenant-slug has invalid char ...`                                 |
| Target tenant exists + carries the slice-205 mark               | (success — idempotent re-run, no changes)                                                                                            |
| Target tenant exists + has > 10 rows + lacks the slice-205 mark | `demoseed: refusing to seed: tenant "..." already has > 10 rows in controls/risks/evidence_records; pick a fresh --tenant-slug`      |
| Target tenant exists + lacks the slice-205 mark                 | `demoseed: refusing to seed: tenant "..." already exists but does not carry the slice-205 forensic mark; pick a fresh --tenant-slug` |

## Anti-criteria honored

- **P0-A1** — opt-in via env var. A fresh production install never receives demo data.
- **P0-A2** — no hard-coded password. Fresh password per invocation, printed once.
- **P0-A3** — no real PII. Names are hand-curated fictional (see `internal/demoseed/names.go`). Domains are `.example`.
- **P0-A4** — refuses to write into a populated or unmarked tenant.
- **P0-A5** — `deploy/docker/bootstrap/seed.sql` is not modified.
- **P0-A6** — writes via the BYPASSRLS pool but every row carries the demo tenant's `tenant_id`; subsequent `atlas_app` reads respect RLS.
- **P0-A7** — slugs use only neutral `demo-*` tokens. No vendor-prefixed test fixture tokens.
- **P0-A8** — passwords + tenant ids + audit-log uuids regenerated per invocation. Re-runs DO NOT produce reproducible-across-invocations sensitive data.

For the full decision log + threat model, see [docs/issues/205-demo-seed-data-comprehensive.md](../issues/205-demo-seed-data-comprehensive.md) and [docs/audit-log/205-demo-seed-data-decisions.md](../audit-log/205-demo-seed-data-decisions.md).
