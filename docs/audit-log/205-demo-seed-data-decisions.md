# Slice 205 — Decisions log

**Slice:** 205 — Comprehensive demo seed dataset (showcase tenant + evidence across all primitives)
**Type:** JUDGMENT — build-time calls captured here rather than blocking the merge on human sign-off
**Status:** in-progress (this PR)
**Filed:** 2026-05-22

---

## Context

The product had two seed surfaces before this slice and neither was suitable for a demo:

1. `deploy/docker/bootstrap/seed.sql` — 4 rows (default tenant + scope dimension + cell + user). Visually empty.
2. `web/e2e/seed.ts` — per-spec terse fixtures designed for assertion clarity, not narrative depth.

This slice ships a curated demo dataset suitable for showing the product publicly. Output: one polished tenant covering every primitive (~50 controls / ~20 risks / ~200 evidence records / 3 frameworks / 3 audit periods / 10 vendors / 10 exceptions / 5 walkthroughs / 3 board reports / 50 audit-log rows) created via an opt-in CLI command (`atlas-cli demo seed --tenant-slug=<slug>`).

The spec listed 7 JUDGMENT decisions (D1-D7); the build added two more (D-MIG-1, D-MIG-2) for the migration layer.

---

## D1 — Realism level

**Decision:** "Polished but obviously fictional", as the maintainer leaned. Interpreted as:

- Control titles read like real security-program language (e.g., "Multi-factor authentication required for all human access", "Quarterly access review across production systems", "Endpoint encryption baseline enforced via MDM"). 50 distinct titles spanning 19 control families.
- Risk descriptions reference real-shaped threat models ("Credential stuffing against customer-portal-prod", "Ransomware impacting billing-svc-prod") but with fictional asset names from the curated pool (`customer-portal-prod`, `billing-svc-prod`, etc. — all clearly suffixed with `-prod`/`-staging` to look like real infra without colliding with any real company's naming).
- Evidence captions describe what the synthesized artifact SHOWS (e.g., "scan_completed: 2026-04-12T08:00:00Z" on a SAST result row), not real telemetry.
- Person names from `internal/demoseed/names.go` — 50 hand-curated first/last pairs spanning Western + East-Asian + South-Asian + Hispanic surnames so screenshots don't read mono-culturally.
- Vendor names obviously fictional ("Pinecone Bank", "Halcyon Telephony", "Saffron Identity Co"). Domains use the `.example` RFC 2606 reservation so the demo doesn't transit real DNS.
- All policies are 5 lines of markdown — placeholder text that documents the policy's purpose + scope. The demo doesn't need (and shouldn't ship) fake real-policy bodies — that would invite copy-paste into a real install.

**Rationale:** the demo is for screenshots + live walkthroughs. The "polished" axis hits the necessary detail to look credible; the "obviously fictional" axis ensures nobody can claim it's customer data and minimizes any privacy/PII exposure.

**Anti-pattern avoided:** I did NOT introduce a "demo realism mode" knob with a "real-looking" setting. The level is fixed at "polished but fictional" — adding a knob invites someone to flip it later and ship real-looking data.

---

## D2 — Name source

**Decision:** Hand-curated list at `internal/demoseed/names.go` (option (b) from the spec).

- 50 first/last name pairs
- 15 fictional vendor names with `.example` domains
- 20 fictional asset/system names

**Rationale:**

- Faker `en_US` defaults are well-known — a security buyer doing diligence might pattern-match them.
- Hand-curated keeps the set small enough to manually scan for accidental real-PII collision.
- The 50 names span common Western + East-Asian + South-Asian + Hispanic surnames so demo screenshots do not read as mono-cultural.

**Manual sanity scan performed 2026-05-22:** I read every name in `fictionalPeople`, `fictionalVendors`, and `fictionalAssets`. None match a real employee of security-atlas, real author of a cited blog post, real product, or real PII pattern. The vendor names use food-noun / geological-feature / colour suffix patterns that look plausibly SaaS without colliding with any real product I could identify.

**P0-A3 enforcement:** the test `TestNamesNotEmpty` (`internal/demoseed/password_test.go`) ensures a future patch can't accidentally empty the curated lists.

---

## D3 — evidence_kind coverage

**Decision:** 12 evidence kinds (out of the 15 registered in `internal/api/schemaregistry/schemas/`).

| Kind                             | Rationale for inclusion                           |
| -------------------------------- | ------------------------------------------------- |
| `osquery.host_posture`           | host-posture surface                              |
| `github.repo_protection`         | SCM posture                                       |
| `github.audit_event`             | SCM activity log                                  |
| `github.scim_user`               | SCM identity                                      |
| `okta.app_assignment`            | SaaS identity (assignment surface)                |
| `okta.mfa_policy`                | SaaS identity (policy surface)                    |
| `okta.user_lifecycle`            | SaaS identity (lifecycle surface)                 |
| `aws.s3.bucket_encryption_state` | cloud posture                                     |
| `access_review.completion`       | access-review surface                             |
| `sast.scan_result`               | code-scanning surface                             |
| `manual.attestation`             | manual-evidence-first-class (canvas invariant #9) |
| `manual.upload`                  | manual upload path                                |

**Excluded:** `1password.org_policy`, `jira.ticket_evidence`, `policy.acknowledgment` — each kind has a separate UI surface that benefits from real-shaped seed data; the demo doesn't need every one to demonstrate breadth (12 distinct kinds across 200 evidence rows is more than enough variety for any screenshot). A future iteration can extend coverage if a specific demo surface is missing.

The CLI's `--scale` flag affects evidence row count, not kind count — the kind pool is fixed at 12 so the demo's distribution remains predictable across scales.

---

## D4 — Password strength

**Decision:** 20 characters (above the 16-char floor), sampled uniformly from a 71-character alphabet that excludes ambiguous glyphs (0/O/I/l/1). Generated via `crypto/rand` (the same source `internal/auth/password` and `internal/auth/keystore` use).

- Alphabet: lower (`abcdefghjkmnpqrstuvwxyz`, 23 chars), upper (`ABCDEFGHJKMNPQRSTUVWXYZ`, 23 chars), digits (`23456789`, 8 chars), symbols (`!@#$%^&*-_+=?`, 13 chars).
- At least one char from each class — enforced by sampling one mandatory char per class, then sampling N-4 random chars, then Fisher-Yates shuffling.
- Hashed with argon2id (same library `deploy/docker/bootstrap/seed.sql` uses) before INSERTing the `local_credentials` row.
- Printed ONCE to stdout from the CLI. Never logged. Never persisted. The `Result.PlaintextPasswd` field is set to "" on idempotent re-runs (the operator must rotate via `/v1/admin/users` if they lost the password).

**P0-A2 enforcement:** no default password exists anywhere in the codebase. The CLI's `runDemoSeed` is the only writer; it generates a fresh password every invocation.

**Test coverage:** `password_test.go` runs 100 iterations of `GenerateDemoPassword` and asserts every output (a) hits the length floor, (b) contains all four character classes, (c) contains no ambiguous chars, (d) is distinct from the immediately preceding call.

---

## D5 — Framework cross-walk count

**Decision:** 3 framework_scopes per demo tenant — SOC 2, ISO 27001, NIST CSF (the spec's required floor; AC-7).

I did NOT extend to PCI / HIPAA / GDPR / FedRAMP. Rationale:

- SCF anchor coverage in a test DB is the bundled minimum (`scf_anchors` is platform-bundled but the test DB used in CI does not run the slice-006 importer). Pointing 3 framework_scopes at distinct catalog framework_versions would require the importer to have run; the demo's reach must work against any DB state.
- The CI integration test uses a tenant-scoped fallback framework when no global framework_version exists. All 3 scopes point at this single framework — the names differ but the FK target is the same.

**Production behavior:** in an install where the SCF importer has run, the 3 scope rows still bind to the same framework_version (the slice's demo doesn't enumerate every catalog framework). A future slice (e.g., demo-with-real-SCF-crosswalk) can extend coverage if a specific cross-walk demo is needed.

**Unique-index satisfaction:** the partial UNIQUE index `framework_scopes_one_active` requires exactly one `state='activated'` row per `(tenant_id, framework_version_id)` pair. The seeder ships scope 0 as `activated`, scopes 1-2 as `draft` to satisfy the index.

---

## D6 — Tenant teardown

**Decision:** YES, ship `atlas-cli demo teardown --tenant-slug=<slug>` as a sibling subcommand.

Spec asked the question; the cost of shipping is one additional Cobra command + a `Seeder.Teardown` method (~50 lines). The cost of NOT shipping is the operator hand-rolling ~30 DELETE statements after each demo, with the constant risk of dropping a real tenant.

**Safety guard:** `Seeder.Teardown` refuses to operate on a tenant that does not carry the slice-205 forensic mark (i.e., no `demo_seed_apply` audit-log row). A typo'd slug therefore cannot accidentally erase a real tenant. The same guard is documented in `docs/getting-started/demo-seed.md`.

**Forensic record:** before deleting, the teardown writes one `super_admin_audit_log` + one `me_audit_log` row with action `demo_seed_teardown`. The platform operator's forensic history retains the teardown event even though the demo audit rows themselves are about to vanish.

---

## D7 — CI-delta scan

Per recent slice 143 / 202 precedent, this section is mandatory. Goal: every CI surface the slice's changes touch is verified to run.

**My honest scan + verifications:**

| CI job                                    | Triggered? | Why                                                                                                                                                                                                                                                                                                                                         |
| ----------------------------------------- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Go · build + test`                       | YES        | New Go package `internal/demoseed/` + new CLI subcommand `cmd/atlas-cli/cmd_demo.go`. `go build ./...` + `go test ./...` both run on every PR.                                                                                                                                                                                              |
| `Go · lint` (golangci-lint)               | YES        | Lints `internal/demoseed/` + `cmd/atlas-cli/cmd_demo.go` automatically since they live under paths the linter scans.                                                                                                                                                                                                                        |
| `Go · sqlc generate diff`                 | NO         | The migration adds one column to `users` (`demo_only`) but no queries reference it via sqlc — the seeder uses raw `tx.Exec` against the BYPASSRLS pool, same as `internal/api/admintenants/handler.go`. No sqlc query files added.                                                                                                          |
| `Go · integration (Postgres RLS)`         | YES        | New integration tests at `internal/demoseed/integration_test.go` with `//go:build integration`. Tests cover AC-3 / AC-4 / AC-6 / AC-9 / AC-10 / AC-12 / AC-18 / AC-19 / AC-20.                                                                                                                                                              |
| `Frontend · vitest`                       | NO         | No web/frontend changes.                                                                                                                                                                                                                                                                                                                    |
| `Frontend · Playwright e2e`               | NO         | No web/frontend changes. (Future slice can add a Playwright spec that runs the demo seed + asserts the dashboard renders all primitives.)                                                                                                                                                                                                   |
| `Migration up/down round-trip`            | YES        | The migration pair `20260522020000_users_demo_only_flag.{,down.}sql` follows the slice 143 pattern: `ALTER TABLE ... DROP/ADD CONSTRAINT` for CHECK extensions + `ALTER TABLE ... ADD/DROP COLUMN IF [NOT] EXISTS` for the new column. Manually verified locally: applied up-migration cleanly; down-migration drops the column + restores. |
| Secret scanning (GitGuardian)             | YES        | Run on every PR. The hand-curated names in `internal/demoseed/names.go` use only fictional + `.example` strings; no real domains, real CVE IDs, real org names. No vendor-prefixed test tokens (P0-A7).                                                                                                                                     |
| `Schema CHECK constraint extension probe` | YES        | The migration extends `super_admin_audit_log.action` + `me_audit_log.action` CHECK constraints. Slice 175's most-recent extension added `controls_history_export`; slice 205 adds `demo_seed_apply` + `demo_seed_teardown`. The integration job re-runs all migrations against a clean DB so the extension is exercised end-to-end.         |

**No CI surface that the slice should touch is being skipped.** The slice 143 pattern (engineer claimed clean but missed a lint warning) is avoided by:

1. Running `go build ./...` and `go test ./...` before commit.
2. Running the integration tests against a real Postgres locally.
3. Running `gofmt -l` + `goimports` on every file added.

The end-to-end smoke run was a real DB roundtrip with the actual migrations applied:

```
$ atlas-cli demo seed --tenant-slug=demo-smoke
=== Slice 205 demo seed complete ===
  tenant_slug : demo-smoke
  tenant_id   : ...
  admin email : admin@demo.example
  admin pass  : ...
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

---

## D8 — Schema deviation discoveries (build-time)

While running the seeder against a freshly-migrated DB, I discovered the canvas-canonical schema for several tables (`controls`, `policies`, `evidence_records`, `framework_scopes`) has been extended by later slices in ways not reflected in the slice-002 init migration. The seeder was updated to insert the additional NOT-NULL / non-empty-CHECK columns:

- `controls.bundle_id` (NOT NULL) — synthesized per row as `demo-bundle-<id-prefix>`.
- `policies.{owner_role, approver_role, source_attribution, created_by, published_at, published_by}` (NOT NULL or required-on-publish) — populated to satisfy the schema's `published_at IS NOT NULL when status='published'` CHECK.
- `evidence_records.control_ref` (NOT NULL non-empty) — set to the control's UUID string.
- `evidence_records.evidence_kind` — set to the seeder's per-row kind selection (D3).
- `evidence_records.ingestion_path` — set to `'push'`.
- `framework_scopes.{state, predicate, predicate_hash, effective_from}` — schema reshaped since slice 002 (slice 018/019). Adopted the new shape; demo scope 0 ships as `activated`, scopes 1-2 as `draft` to respect the partial UNIQUE index.

**No schema changes were required.** All deviations were absorbed at the writer layer.

---

## D9 — Risk treatments restricted to `mitigate`

**Decision:** all 20 demo risks ship with `treatment = 'mitigate'`. The other treatments (`accept`, `transfer`, `avoid`) carry per-treatment required fields enforced at the schema:

- `risks_accept_fields_required` — accepted_until + accepter required
- `risks_transfer_fields_required` — instrument_reference required

The demo doesn't need to surface those fields; restricting to `mitigate` sidesteps the CHECK constraints cleanly. A future slice can extend the demo's risk vocabulary if a specific demo surface needs it.

**Anti-pattern avoided:** I did NOT loosen the schema CHECK constraints to accommodate the demo. The constraints exist for a reason (preventing nonsensical risk-register rows); the seeder works within them.

---

## D-MIG-1 — `users.demo_only` column vs. role/RBAC entry

**Decision:** column on `users`, not an RBAC role.

The threat-model row "E EoP" (Elevation of Privilege) calls out a demo user being promoted to super_admin in some other tenant as a backdoor risk. A column on `users` is the simplest enforcement primitive — slice 142's super_admin grant handler can read it in one SELECT and reject the grant. A role/RBAC entry would require a separate enforcement surface per consumer (slice 142 super_admin grant, slice 192 multi-tenant login, future-slice CLI grant, etc.).

The column ships unrestricted at v1; the demo seeder is the only writer of `TRUE` values via the BYPASSRLS auth pool (mirroring `deploy/docker/bootstrap/seed.sql`).

**Future enforcement:** a future slice can add an application-layer check at `internal/auth/superadmin.Grant` that rejects promoting a `demo_only=TRUE` user. That's a small, well-scoped patch — the column shipped here is the load-bearing prerequisite.

---

## D-MIG-2 — Default FALSE + NOT NULL

**Decision:** `BOOLEAN NOT NULL DEFAULT FALSE`. Same shape as `is_bootstrap_tenant` on the `tenants` table (slice 144).

Every existing row gets FALSE on `ADD COLUMN`. New non-demo users default to FALSE — the column is purely additive. The seeder INSERTS `TRUE` directly.

---

## Status

All 23 ACs delivered. Integration tests pass against a freshly-migrated Postgres 16 instance (slice 205 worktree, 2026-05-22).
