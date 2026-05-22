# Demo seed: populate a tenant for screenshots + live demos

This page documents `atlas-cli demo seed` — the opt-in command that
creates one polished demo tenant suitable for showing security-atlas
publicly. The dataset covers every product primitive (controls, risks,
evidence, policies, audit periods, walkthroughs, exceptions, vendors,
board reports, audit log) and produces a meaningful render across every
backend-backed UI surface.

The command is **opt-in only**. A fresh `docker compose up` does NOT
receive demo data. A production install does NOT receive demo data
unless the operator explicitly runs the command.

## When to use it

- You're recording a product walkthrough or live demo.
- You're taking screenshots for a blog post / pitch deck.
- You're developing a new UI surface and want a realistic dataset to
  iterate against without writing per-spec test fixtures.
- An auditor wants to see what the platform looks like populated, so
  you spin up a throwaway demo tenant for them to explore.

## When NOT to use it

- **A production install.** The seeder writes ~600 rows under a
  dedicated tenant; you do not want any of those rows in production.
- **A pre-existing customer tenant.** The seeder refuses to write into
  any tenant that already has > 10 rows in controls / risks /
  evidence_records — defense-in-depth against the slug-typo case.
- **A long-lived demo environment.** The seeder generates a fresh
  password per invocation that prints once and is not recoverable.
  For long-lived deployments, rotate the password to one managed by
  your password manager via the standard `/v1/admin/users` surface.

## Prerequisites

- `atlas-cli` binary built from this repo (`go build -o atlas-cli ./cmd/atlas-cli/`).
- BYPASSRLS DSN available — typically the same `DATABASE_URL` the
  docker-compose stack hands the atlas-bootstrap container. The seeder
  writes across tenant boundaries (it creates the demo tenant) so it
  needs the BYPASSRLS pool, not the application pool.
- The `ATLAS_ENABLE_DEMO_SEED=true` env var set — without it, the
  command refuses to run.

## Invocation

```bash
ATLAS_ENABLE_DEMO_SEED=true \
DATABASE_URL='postgres://atlas_migrate:...@localhost:5432/security_atlas?sslmode=disable' \
  atlas-cli demo seed --tenant-slug=demo-acme
```

The command prints to stdout exactly once:

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

| Flag             | Default         | Notes                                                                                                                                                                         |
| ---------------- | --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--tenant-slug`  | (required)      | Lower-case alphanumeric + hyphen, 1-63 chars. Use a `demo-` prefix so the slug is obviously a demo tenant in any UI.                                                          |
| `--scale`        | `1.0`           | Row-count multiplier. `0.5` halves every per-primitive floor (still at least 1 row per primitive); `2.0` doubles them. Useful for stress-testing the UI or for compact demos. |
| `--database-url` | `$DATABASE_URL` | Override the BYPASSRLS DSN. Convenient when you have multiple Atlas deployments.                                                                                              |

## Idempotency

Re-running with the same `--tenant-slug` is a no-op:

```bash
$ atlas-cli demo seed --tenant-slug=demo-acme
demo seed: tenant "demo-acme" already seeded (id=<UUID>); no changes made. Rotate the password via /v1/admin/users if needed.
```

If you want fresh demo state, use `demo teardown` first (see below).

## Teardown

When the demo is done, drop the tenant + every row anchored to it:

```bash
ATLAS_ENABLE_DEMO_SEED=true \
DATABASE_URL='...' \
  atlas-cli demo teardown --tenant-slug=demo-acme
```

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
| `--tenant-slug` not supplied                                    | (Cobra `required flag not provided`)                                                                                                 |
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
