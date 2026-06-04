# 205 — Comprehensive demo seed dataset (showcase tenant + evidence across all primitives)

**Cluster:** Backend (seed / fixtures) + Quality (docs)
**Estimate:** 3d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** maintainer-surfaced 2026-05-22. The product has no curated demo dataset — fresh installs land on `deploy/docker/bootstrap/seed.sql` which seeds the bare minimum (1 tenant, 1 user, 1 scope dimension, 1 scope cell). Slice 082's `web/e2e/seed.ts` produces per-spec test fixtures but they're terse and tuned for test assertions, not narrative. Operators wanting to demo security-atlas have nothing to show.

## Narrative

The product currently ships two seed surfaces and neither is suitable for a demo:

1. **`deploy/docker/bootstrap/seed.sql`** (slice 037 first-boot) — 4 rows: default tenant + 1 scope dim + 1 scope cell + 1 user. Functional but visually empty. Dashboard, controls page, risks page, evidence page all render with "no data".

2. **`web/e2e/seed.ts` + `fixtures/e2e/*.sql`** (slice 082) — per-spec terse rows. Designed for assertion clarity, not narrative depth: 2 controls, 1 risk, 1 sample period. Demoing off this looks like a toy system.

**This slice ships a curated demo dataset** suitable for showing the product publicly. The output is:

1. A new CLI command `atlas-cli demo seed [--tenant-slug=<slug>]` that, when invoked against an atlas instance with the right credentials, populates ONE NEW dedicated tenant with comprehensive, realistic demo data. The command is idempotent (re-runs replace, don't accumulate) and explicitly opt-in (NOT auto-run on bootstrap).

2. The seed dataset itself, expressed as Go fixtures (similar to `internal/policy/seed/` and `internal/catalog/metrics/seed.go`) at `internal/demoseed/`. The dataset covers every primitive listed in `Plans/canvas/02-primitives.md` — Control, Risk, Evidence, Scope, Framework, Policy — plus the secondary entities the product surfaces (audit periods, samples, walkthroughs, exceptions, board reports, audit-log entries, vendor records).

3. Documentation at `docs/getting-started/demo-seed.md` explaining the seed command, its scope, the tenant it creates, the credentials to demo with, and the operator workflow for tearing it down post-demo.

**The judgment call**: how realistic does the data need to be? Maintainer's lean (filed 2026-05-22) is **"polished but obviously fictional"** — realistic enough that screenshots look credible to a security buyer, fictional enough that nobody can claim it's customer data. The implementing engineer makes detail-level calls (which controls? which frameworks? how many of each?) and documents them in D1-DN.

**Scope discipline (what is OUT):**

- **NOT auto-run on bootstrap.** A fresh production install must NOT receive demo data. The CLI command is explicitly opt-in. This is P0.
- **NOT a multi-tenant demo.** One curated tenant. A future slice can add cross-tenant demo (e.g., showing tenant-switcher chrome from slice 141), but multi-tenant complicates the dataset 3x — defer.
- **NOT live connector data.** All evidence is synthesized; no AWS / GitHub / Okta API calls. A future slice can wire demo connectors that read from a fixture-S3-bucket if needed.
- **NOT a board-report PDF.** A future slice handles polished PDF generation. This slice ships a board-report ROW with the underlying narrative/metrics so the in-app `/board/preview` page renders; PDF export remains live-generated.
- **NOT performance-tuned for large scale.** ~50 controls / ~20 risks / ~200 evidence records / ~5 policies / ~3 audit periods. Sized to be browsable, not stress-tested.
- **NOT the "missing" UI screens.** If a mockup page exists but has no backend (per slice 204 audit findings), this slice does NOT add backend code to back it. The demo seed populates what the BACKED surfaces need; mockup-only surfaces remain empty.
- **NOT a privacy review.** Synthetic data only; names of fictional companies + people generated from a documented fictional-name source (`Faker`-style, no real-PII risk). Document the source in D2.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                            | Mitigation                                                                                                                                                                                                                                                                                                                                                                                          |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Demo tenant user credentials. If the CLI ships a pre-seeded password, the credential becomes a public secret on every demo install — drive-by attackers exploit deployments still running demo creds in production.               | AC-12: CLI generates a fresh strong password per invocation, writes it to stdout exactly once, and does NOT log it anywhere persistent. Operator captures it; no default password exists. The seed user is also created with the `demo-only` role flag (new in this slice — a simple boolean column on `users`, not a role/RBAC entry) so it cannot be promoted to admin via `/admin/super-admins`. |
| **T** Tampering       | Demo seed inserts via atlas_migrate (BYPASSRLS) and writes to every domain table. A bug in the seed could corrupt cross-tenant data — write demo data into a production tenant by accident.                                       | AC-3: CLI requires `--tenant-slug` argument; refuses to run against any tenant that already has > 10 rows in any of (controls / risks / evidence). The "fresh tenant" guard. AC-13: the seed creates a NEW tenant; it does not write into an existing tenant. Slug uniqueness via slice 143's partial UNIQUE index prevents collision.                                                              |
| **R** Repudiation     | Demo data includes audit-log entries (the demo shows the audit-log surface working). Those rows are synthesized; they could be confused for real operator actions during a forensic review post-demo.                             | AC-9: every synthesized audit-log row has `actor_id` set to the seeded demo user's UUID and `payload_json->>'$.demo_seed_v'` set to the slice version (e.g. `"205"`). A forensic query can `WHERE payload_json ? 'demo_seed_v'` to filter out demo noise. AC-21: docs at `docs/getting-started/demo-seed.md` document this filter convention.                                                       |
| **I** Info disclosure | Demo data contains fictional company / person / control / vendor names. If any are accidentally PII-shaped (real names, real org names, real CVE attributions to wrong vendors), demoing in public could embarrass real entities. | AC-2: all fictional names sourced from a deterministic, documented fictional-name generator (e.g. seeded Faker `en_US` locale OR a hand-curated `~50` name list at `internal/demoseed/names.go`) AND a one-line manual sanity scan during build (the engineer reads the generated names list and confirms none look real). Document the source + scan in D2.                                        |
| **D** DoS             | If `atlas-cli demo seed` is run repeatedly without idempotency, it could fill the DB with duplicate demo tenants until disk fills.                                                                                                | AC-4: idempotent — re-running with the same `--tenant-slug` UPDATES the existing demo tenant in place. Different slug → new tenant. The "10-row guard" (AC-3) also blocks against accidentally writing into already-populated tenants.                                                                                                                                                              |
| **E** EoP             | Demo user could be inadvertently granted super_admin or admin in some other tenant. A demo install promoted to a real-tenant install could carry the demo user forward as a backdoor.                                             | AC-11: demo user is created with `role=admin` in the demo tenant only, never in any other tenant. AC-12 (above): `demo-only` flag prevents super_admin promotion via the slice 142 management surface. Future slice may strengthen this: a startup check that refuses to boot atlas in `ATLAS_DEPLOYMENT_MODE=production` if any user has `demo-only=true`.                                         |

## Acceptance criteria

### Seed dataset (Go fixtures)

- [ ] **AC-1**: New Go package at `internal/demoseed/` with one file per primitive area: `controls.go`, `risks.go`, `evidence.go`, `policies.go`, `framework_scopes.go`, `audit_periods.go`, `samples.go`, `walkthroughs.go`, `exceptions.go`, `board_reports.go`, `audit_log.go`, `vendors.go`. Each file exports a `Fixture()` function returning the rows to insert.
- [ ] **AC-2**: All fictional names sourced from a documented generator. Engineer picks: (a) seeded Faker `en_US` locale, (b) hand-curated list at `internal/demoseed/names.go`, or (c) hybrid. Choice + rationale documented in D2.
- [ ] **AC-3**: CLI `atlas-cli demo seed --tenant-slug=<slug>` refuses to run if any of (controls / risks / evidence) in the target tenant has > 10 rows. Error message points the operator at the slug-uniqueness guard.
- [ ] **AC-4**: Idempotent. Re-running with the same `--tenant-slug` updates the existing demo tenant. Different slug creates a new tenant.
- [ ] **AC-5**: Dataset sizing: ~50 controls, ~20 risks, ~200 evidence records, ~5 policies, ~3 audit periods (1 frozen, 2 open), ~10 vendors, ~10 exceptions, ~5 walkthroughs, ~3 board reports, ~50 audit-log rows. Tunable by `--scale` flag (defaults to 1.0; 2.0 doubles everything; 0.5 halves).
- [ ] **AC-6**: Coverage breadth: every primitive listed in `Plans/canvas/02-primitives.md` has at least one row. Every `evidence_kind` registered in the schema registry (per slice 003's catalog) has at least one evidence record. Documented in D3.
- [ ] **AC-7**: Framework spread: demo tenant has `FrameworkScope` rows for at least 3 frameworks (SOC 2, ISO 27001, NIST CSF). Each framework's STRM crosswalks (via slice 006's SCF importer + slice 011's UCF graph) produce realistic crosswalk coverage in the UI.
- [ ] **AC-8**: Evidence temporal variety: evidence `observed_at` timestamps spread across the prior 12 months. ~30% within last 7 days (fresh), ~40% within last 30 days, ~20% 30-90 days, ~10% > 90 days (stale). Enables the dashboard's freshness UI (slice 016) to render meaningful gradients.
- [ ] **AC-9**: Every synthesized audit-log row sets `payload_json->>'$.demo_seed_v'` = `"205"` for forensic filtering (threat-model R).
- [ ] **AC-10**: Audit-period freezing demonstrated: 1 of the 3 audit periods is `frozen=true` with a frozen sample population (~10 evidence records pinned at freeze time). Subsequent evidence `observed_at` values land AFTER the frozen_at timestamp, so the audit period's sample remains coherent.

### CLI command

- [ ] **AC-11**: New subcommand `atlas-cli demo seed --tenant-slug=<slug> [--scale=N.N]`. Lives at `cmd/atlas-cli/demo.go` per existing CLI layout.
- [ ] **AC-12**: Demo user creation: fresh strong password (≥16 chars, mixed case + digits + symbols) generated per invocation, written to stdout exactly once, NEVER logged or written to file. User created with `role=admin` in the demo tenant and a new `demo_only=TRUE` flag on the `users` row (migration adds the column; default FALSE for all existing rows). Documented in D4.
- [ ] **AC-13**: CLI creates a NEW tenant (insert via slice 143's atomic write path) named `<slug>` with display name `"<slug> Demo"`. Does not overwrite or write into an existing non-demo tenant.
- [ ] **AC-14**: CLI is gated by an env var `ATLAS_ENABLE_DEMO_SEED=true` (or equivalent). Refuses to run if the env var is unset, with a clear error pointing at the docs. P0-A1's enforcement.
- [ ] **AC-15**: CLI exits 0 on success with a final status line containing the tenant slug + user email + (one-time-printed) password.

### Migration

- [ ] **AC-16**: New migration `migrations/sql/<NNNN>_users_demo_only_flag.sql` adds `users.demo_only BOOLEAN NOT NULL DEFAULT FALSE`. Reversible (`.down.sql` drops the column).
- [ ] **AC-17**: `super_admin_audit_log` and `me_audit_log` action CHECK constraints extended with `demo_seed_apply` and `demo_seed_teardown`. CLI invokes both at start + end of seed runs.

### Tests

- [ ] **AC-18**: Go integration test at `internal/demoseed/integration_test.go`: spins up a clean test DB, runs `atlas-cli demo seed --tenant-slug=demo-test`, asserts row counts in every domain table match the ~50/~20/~200/etc. floor. Cross-tenant isolation test: a second tenant created during the same test run sees ZERO demo rows.
- [ ] **AC-19**: Idempotency integration test: run seed twice with the same slug, assert row counts are unchanged after the second run.
- [ ] **AC-20**: Refusal test: pre-populate a tenant with > 10 controls, then run seed with that tenant's slug, assert the CLI exits non-zero with the expected error message.

### Docs + audit-log

- [ ] **AC-21**: New doc at `docs/getting-started/demo-seed.md` covering: when to use, when NOT to use, CLI invocation example, where the password gets printed, how to tear down (drop the tenant), the `payload_json ? 'demo_seed_v'` forensic-filter convention.
- [ ] **AC-22**: CHANGELOG entry under `Features`.
- [ ] **AC-23**: Decisions log at `docs/audit-log/205-demo-seed-data-decisions.md` with D1 (overall realism level), D2 (name source), D3 (which evidence_kinds got rows), D4 (password generation library + strength choice), D5 (any P0 anti-criteria interpretations the engineer had to make), DN (further JUDGMENT calls).

## Constitutional invariants honored

- **#1 One control, N framework satisfactions**: demo controls map to SCF anchors; each anchor has the cross-walks slice 006 imported. The demo respects the no-duplicate-controls-per-framework invariant.
- **#2 Ingestion / evaluation separated; append-only evidence**: demo evidence rows are written via the standard ingestion path (no shortcut around the ledger).
- **#6 RLS at the database layer**: demo seed writes via `atlas_migrate` (BYPASSRLS, like bootstrap); every row has `tenant_id` set. Subsequent reads via `atlas_app` MUST respect RLS — AC-18's cross-tenant test enforces this.
- **#9 Manual evidence is first-class**: demo includes both auto-pushed evidence (`evidence_kind=osquery.host_posture.v1`, etc.) and manual evidence (a few rows tagged `source=manual` per slice 005's manual surface).

## Canvas references

- `Plans/canvas/02-primitives.md` — defines the six core primitives the demo must cover
- `Plans/canvas/04-evidence-engine.md` — evidence kinds + manual evidence surface
- `Plans/canvas/05-scopes.md` — FrameworkScope intersection (the demo demonstrates this)
- `Plans/canvas/07-metrics.md` — board-report metrics surface
- `Plans/canvas/08-audit-workflow.md` — audit-period freezing (AC-10)

## Dependencies

- **#143** (create-tenant flow) — merged. CLI uses the same atomic-write path for the new tenant.
- **#011** (UCF / SCF crosswalks) — merged. Demo controls map onto SCF anchors that already exist.
- **#016** (evidence freshness / drift) — merged. AC-8's temporal spread feeds this surface.
- **#028** (audit-period freezing) — merged. AC-10 exercises this.
- **#082** (Playwright seed harness) — merged. The demo seed is conceptually adjacent but distinct: it produces ONE polished tenant, not per-spec fixtures. Document the relationship in D1.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT auto-run on bootstrap. The CLI is opt-in, env-var-gated (`ATLAS_ENABLE_DEMO_SEED=true`). A fresh `docker compose up` MUST NOT receive demo data. AC-14 enforces.
- **P0-A2**: DOES NOT ship hard-coded credentials. AC-12: fresh password per invocation, printed once, never persisted.
- **P0-A3**: DOES NOT contain real PII (real names, real org names, real email addresses, real CVE→vendor attributions). AC-2's name-source documentation + the manual sanity scan are the gate.
- **P0-A4**: DOES NOT write into an existing populated tenant. AC-3's >10-row guard + AC-13's new-tenant constraint enforce.
- **P0-A5**: DOES NOT modify or extend the bootstrap seed at `deploy/docker/bootstrap/seed.sql`. The bootstrap is sacred — it's what fresh production installs see. Demo seed is a separate command.
- **P0-A6**: DOES NOT bypass RLS at runtime. Seed writes via `atlas_migrate` (BYPASSRLS) at insert time — same pattern as bootstrap; subsequent reads via `atlas_app` respect RLS. AC-18 verifies.
- **P0-A7**: DOES NOT use vendor-prefixed test fixture tokens. Neutral `demo-*` only.
- **P0-A8**: DOES NOT silent-mode write to disk any credential, key, or audit-log entry that would be reproducible across invocations. Password is one-time-printed only; audit-log rows have `demo_seed_v=205` for filtering.

## Skill mix

- Go fixture authoring (`internal/policy/seed/`, `internal/catalog/metrics/seed.go` are reference templates)
- CLI subcommand pattern (`cmd/atlas-cli/` existing layout)
- Migration design (one column add, idempotent)
- Postgres bulk-insert patterns + RLS-bypass via `atlas_migrate`
- Strong-password generation (Go `crypto/rand`; document the alphabet + length)
- Faker-style name generation OR hand-curated fictional-name list curation

## Notes for the implementing agent

This is a JUDGMENT slice — many design calls the engineer makes during build. Document each in the decisions log:

- **D1: realism level.** "Polished but obviously fictional" is the maintainer's lean. Engineer interprets: control narratives are 1-2 sentences of realistic security-program language; risk descriptions reference real-shaped threat models (e.g., "credential stuffing against customer portal") but with fictional company / asset names; evidence captions describe what the synthesized artifact SHOWS, not real telemetry.

- **D2: name source.** Pick Faker `en_US` (deterministic with a fixed seed) OR hand-curated. Faker is faster to author; hand-curated avoids the "always the same 100 Faker names" pattern-match risk. Recommend hand-curated for ~50 names — small enough to scan, distinct enough to look intentional.

- **D3: evidence_kind coverage.** The schema registry has many evidence kinds (see `internal/schemaregistry/`). The demo doesn't need every one — pick ~8-12 that span the major surfaces (host posture, SaaS posture, access reviews, SAST scans, vulnerability scans, change reviews, manual). Document which ones got rows + why.

- **D4: password strength.** ≥16 chars is the floor. Use a clear alphabet (no ambiguous chars `O/0/I/l/1`). Pick a documented library or roll a small generator with `crypto/rand` from a curated alphabet.

- **D5: framework cross-walk count.** AC-7 says "at least 3 frameworks". The engineer can ship more if the SCF anchor coverage supports it without expensive lookups. Document the cap they picked + why.

- **D6: tenant tear-down**. Should the CLI ship a `atlas-cli demo teardown --tenant-slug=<slug>` companion? AC-21 documents the operator workflow as "drop the tenant" but a CLI command would be safer. Engineer decides + documents; if shipped, it's a single additional subcommand at low cost.

- **D7: CI-delta scan.** Per recent slice 143/202 pattern, explicitly verify the CI delta this slice introduces. Specifically: does the new migration affect `Go · sqlc generate diff`? Does the new CLI subcommand affect `Go · build + test`? Document findings.

**Slice 204 operational note**: this slice's deliverable is a populated tenant — the kind slice 204's audit fleet would love to audit against. Slice 205 + slice 204 compose well: once 205 ships, an operator can `atlas-cli demo seed --tenant-slug=audit-target` and then dispatch slice 204's fleet against the resulting populated UI.

**The 500-error class (per maintainer 2026-05-22) is upstream of this slice.** If the v1.14.0 deployment can't render pages due to auth-substrate-v2 config gaps, the demo seed populates rows that nobody can see in the UI. Make sure the local dev build renders pages before claiming the demo seed works.

Provenance: filed 2026-05-22 via `/idea-to-slice` from maintainer's "demo site" request. Builds atop the slice 037 bootstrap pattern (writes via atlas_migrate; tenant-scoped) and slice 082 e2e seed pattern (Go-fixture-driven). The novelty is breadth — one polished tenant covering every product surface — and the public-demo-deployment context (P0-A2 + P0-A3 are the load-bearing guards).
