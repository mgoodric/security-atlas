# 059 — Per-tenant feature flags + capability toggles

**Cluster:** Spine / tenant configuration
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement per-tenant feature flags so operators can turn entire capability areas on or off — risk register, vendor management, policy library, control-as-code, OSCAL export, board reporting, audit workflow, evidence freshness, exceptions, theme tagging, etc. Real GRC programs run alongside existing tools (a company may already use OneTrust for vendor management or Jira for risk tracking) — forcing every capability on is a false-binary that scares adopters off. A small `feature_flags` table + middleware that 404s disabled-capability routes + a `featureflag.Enabled(ctx, key)` Go helper give operators surgical control without sprawling configuration.

Flags are **per-tenant**, **default-on for most capabilities**, and **always-on for spine capabilities** (controls, evidence ledger, scope, framework crosswalks, auth, RLS — anything in the v1 spine is non-toggleable). Flag toggles are admin-only and audit-logged. The product surface degrades cleanly: disabled-capability routes return `404 Not Found` (no leak that the feature exists); disabled-capability dashboard cards hide; CLI commands that target disabled capabilities print a clear "feature is disabled in this tenant" message and exit 0 (not an error — the operator chose this).

The slice delivers value because adopters can run security-atlas against the slice of their program that doesn't have an existing tool, rather than being forced to migrate everything at once.

## Acceptance criteria

- [ ] AC-1: `feature_flags` table per tenant: `tenant_id`, `flag_key` (text, snake_case namespaced), `enabled` (bool), `description`, `category` (text — `core` / `risk` / `vendor` / `policy` / `controls` / `audit` / `evidence` / `board` / `integrations`), `last_changed_by`, `last_changed_at`. Composite primary key `(tenant_id, flag_key)`. Four-policy RLS under FORCE.
- [ ] AC-2: Default flag seed: ~12 capability flags shipped with sensible defaults (most `true`, integrations `false`). Seed lives in a separate idempotent migration so re-running is a no-op.
- [ ] AC-3: Spine flags do NOT exist — invariants like RLS, tenancy, auth, schema registry, scope, evidence ledger, framework crosswalks are non-toggleable. The seed list explicitly omits these.
- [ ] AC-4: Go helper `internal/featureflag.Enabled(ctx, key)` returns `(bool, error)`. Looks up the row using the current tenant context (slice 033's `tenancy.Middleware`-set GUC). Falls back to the seed default if no row exists. Memoizes within the request lifetime via context (avoid N+1 lookups per request).
- [ ] AC-5: HTTP middleware `featureflag.Gate(key)` wraps a route; when disabled returns `404 Not Found` with body `{"error": "feature disabled"}` and an `X-Feature-Disabled: <key>` header for observability.
- [ ] AC-6: `GET /v1/admin/features` — admin-only — returns the full flag list with current state.
- [ ] AC-7: `PATCH /v1/admin/features/{key}` — admin-only — toggles `enabled`. Stores the change in `feature_flag_audit_log` (append-only).
- [ ] AC-8: CLI `atlas-cli features list` / `atlas-cli features set <key> <on|off>` — admin-only — same operations from the command line.
- [ ] AC-9: Integration test: a disabled flag returns 404 on its gated route; toggling re-enables the route in the next request without restart.
- [ ] AC-10: Audit-log integration test: every toggle writes one `feature_flag_audit_log` row with `actor`, `flag_key`, `from`, `to`, `at`.

## Constitutional invariants honored

- **Invariant 6** (RLS): the `feature_flags` table is tenant-scoped with the four-policy RLS pattern; `feature_flag_audit_log` is append-only (`tenant_read` + `tenant_write` only under FORCE).
- **Spine invariants stay non-toggleable**: AC-3 enforces this at the seed level + a unit test that asserts no spine key appears in the seed list.
- **AI-assist boundary**: feature-flag toggles are human-driven (admin clicks or CLI). No AI auto-flips flags.

## Canvas references

- `CLAUDE.md` (constitutional principles · spine invariants)
- `Plans/canvas/02-primitives.md` (which entities are spine vs capability)
- `Plans/canvas/10-roadmap.md` §10.1 (v1 capability inventory — informs the seed list)

## Dependencies

- **002** (schema spine + tenancy)
- **033** (RLS enforcement + tenancy middleware — flag lookup uses the GUC)
- **034** (auth — admin-only gate on the flag CRUD endpoints)

## Seed flag inventory (proposed)

| Flag key                 | Category     | Default | Gates                                                     |
| ------------------------ | ------------ | ------- | --------------------------------------------------------- |
| `risk.enabled`           | risk         | `true`  | `/v1/risks/*`, `/v1/risks/aggregate`                      |
| `risk.themes`            | risk         | `true`  | `/v1/themes`, `/v1/risks/{id}/themes`                     |
| `risk.hierarchy`         | risk         | `true`  | `/v1/org_units/*`                                         |
| `vendor.enabled`         | vendor       | `true`  | `/v1/vendors/*`                                           |
| `policy.enabled`         | policy       | `true`  | `/v1/policies/*`                                          |
| `policy.acknowledgments` | policy       | `true`  | `/v1/me/acknowledgments`, `/v1/policies/{id}/acknowledge` |
| `controls.bundles`       | controls     | `true`  | `/v1/control-bundles/*` (slice 009)                       |
| `exceptions.enabled`     | controls     | `true`  | `/v1/exceptions/*` (slice 021)                            |
| `audit.workflow`         | audit        | `true`  | `/v1/populations`, `/v1/samples` (slice 026)              |
| `oscal.export`           | integrations | `false` | `/v1/oscal/export/*` (future slice 030)                   |
| `board.reporting`        | board        | `false` | `/v1/board/*` (future slices 031/032)                     |
| `decisions.log`          | risk         | `true`  | `/v1/decisions/*` (future slice 055)                      |

## Anti-criteria (P0 — block merge)

- Does NOT permit toggling spine flags. The seed list MUST exclude any key in the spine-invariant inventory (controls, evidence, scope, framework, auth, RLS, tenancy). A unit test enforces this.
- Does NOT silently fail closed when the DB is unreachable. The helper returns the seed default + logs a warning; "feature flag DB read failed" is not a security boundary, RLS is.
- Does NOT permit non-admin role to write flags. Reads are admin-only too (the flag state could expose feature surface to attackers); use slice 034 cred.IsAdmin gate.
- Does NOT cache flag values across requests at process level. In-request memoization is fine; cross-request caching would create stale-state confusion when toggles happen.

## Skill mix (3–5)

- Postgres schema + migration discipline
- Go middleware + context plumbing
- sqlc query layer
- CLI design (cobra)
- Audit-log discipline (append-only RLS)
