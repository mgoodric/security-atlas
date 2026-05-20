# Slice 180 — Privacy-module foundation: decisions log

**Filed:** 2026-05-20
**Slice:** [`docs/issues/180-privacy-module-foundation.md`](../issues/180-privacy-module-foundation.md)
**Parent canvas resolution:** [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md) item #7, resolved 2026-05-20

This log captures the JUDGMENT calls Claude made during slice 180's build —
the decisions the slice spec deliberately left to the implementing agent's
judgment, recorded here so the maintainer can re-litigate after merge if
any prove wrong in retrospect.

---

## D-180-1. OQ #7 resolution shape (recorded for traceability)

**Decision context.** Canvas OQ #7 resolved 2026-05-20 as **Option B —
sibling module**. Four sub-decisions locked:

- **B1 — Postgres schema isolation.** Privacy primitives live in a dedicated
  `privacy.*` Postgres schema namespace (clean DB-level boundary;
  `pg_dump --schema=privacy` works as a backup unit).
- **B2 — Shared infrastructure.** OIDC + tenancy / RLS + audit-log ledger
  (extended with the slice 180 `subject_module` column) + feature-flag
  system. NOT a separate deployment.
- **B3 — Cross-module reference seam.** Privacy tables MAY FK
  `evidence.id` + `policy.id`; MUST NOT FK `controls.id` directly (privacy
  → security mapping happens at the framework-satisfaction layer per
  GDPR Art. 32, not at the data-flow layer).
- **B4 — Lint-rule enforcement.** A CI check fails any PR touching BOTH
  `internal/api/privacy/**` AND `internal/api/controls/**`. Escape-hatch:
  `[cross-module-ok]` PR label.

**Rationale (the deep reason).** Privacy regulations evolve on their own
cadence (faster than security frameworks); sibling-isolation lets the
privacy module's schema evolve without forcing core migrations. Privacy
v0 ships at v2+ when a real prospect surfaces demand; **slice 180 is the
foundation work that makes the sibling shape land cleanly when privacy v0
fires**.

---

## D-180-2. Why no `privacy` Postgres schema namespace yet

**Decision.** Slice 180 does NOT execute `CREATE SCHEMA privacy`.

**Why.** Empty schemas are confusing — operators inspecting the database
would see `\dn` show a `privacy` schema with zero tables and reasonably
wonder if something is missing or broken. The slice spec's anti-criterion
**P0-180-1** explicitly forbids this for the same reason.

**When it lands.** The privacy v0 slice (gated on a real prospect surfacing
demand) creates the schema in its own migration alongside the first
privacy table. The migration sequence in v0 will be:

```sql
CREATE SCHEMA IF NOT EXISTS privacy;
GRANT USAGE ON SCHEMA privacy TO atlas_app, atlas_migrate;
GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA privacy TO atlas_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA privacy GRANT SELECT, INSERT, UPDATE ON TABLES TO atlas_app;
CREATE TABLE privacy.data_subjects ( ... );
-- etc.
```

The shape of those grants is documented here as guidance for the v0
engineer — it is NOT executed in slice 180.

---

## D-180-3. Why no lint rule yet

**Decision.** Slice 180 does NOT add the B4 CI lint rule.

**Why.** With no `internal/api/privacy/` directory existing in the
repository today, the rule has nothing to enforce. Adding it now means
the CI job either (a) silently passes on every PR forever — useless
signal that erodes trust in the check — or (b) requires hand-crafted
fixtures to keep it honest, which is engineering effort spent on a
rule that lints nothing.

**When it lands.** Alongside the first `internal/api/privacy/` files in
privacy v0. The v0 PR is the natural place to add a non-trivial test that
deliberately violates the rule (touching both packages) and asserts the
rule blocks the PR.

**Shape it will take (recorded for the v0 engineer).** A GitHub Actions
job consuming `git diff --name-only origin/main...HEAD` that fails if
the diff matches BOTH path globs, unless the `[cross-module-ok]` label
appears on the PR. The check is informational at first (not in
branch-protection's required set) for a 4-week soak; promote to required
in a follow-up slice once the operator workflow is settled.

---

## D-180-4. Slice 124 coordination outcome — **124-before-180 path**

**Decision context.** Slice 180's spec AC-8 anticipated two orderings:

- **180-before-124 path** (predicted as "most likely given slice 124 is
  solo JUDGMENT 3-4d"): 180 lands first; 124's engineer picks up the new
  column in their UNION at pickup time.
- **124-before-180 path:** 180 updates 124's UNION query in its PR.

**Actual state on 2026-05-20.** Slice 124 is **merged** (PR `gh#267`,
batch 46, squash-merged 2026-05-18). The `_STATUS.md` row for slice 124
records `merged` with the full UNION ALL across all nine tables
landed and the `internal/api/adminauditlog/unified.go` HTTP handler in
production. The 180-before-124 prediction was wrong — slice 124 was
faster than estimated.

**This slice's response — 124-before-180 path.**

- Updated `internal/db/queries/unified_audit_log.sql` (the slice 124
  canonical UNION query) so every one of the nine UNION branches reads
  `subject_module` from its base table and projects it through the CTE's
  final SELECT alongside the existing canonical columns.
- Updated the `subject_module` payload-exclusion list for each branch's
  `to_jsonb(...) - '...'` payload constructor so the column doesn't get
  double-counted (once as a top-level column AND embedded inside
  `payload_json`).
- Regenerated `internal/db/dbx/unified_audit_log.sql.go` via
  `just sqlc-generate`; the `ListUnifiedAuditLogRow` struct now carries
  `SubjectModule string`.
- Updated `internal/audit/unifiedlog/unifiedlog.go`'s `Entry` struct to
  expose `SubjectModule string` and the `Query` mapper to read the new
  sqlc field.
- Updated `internal/api/adminauditlog/unified.go`'s `UnifiedEntry` wire
  shape to include `subject_module` on the HTTP response.

**Tests.** The slice 124 integration test
(`internal/api/adminauditlog/unified_integration_test.go`) does not pin
specific column names on the response — it asserts `Kind` + `TenantID`
counts only — so it continues to pass unchanged. The slice 180 integration
test (`internal/db/subject_module_integration_test.go`) lives in
`internal/db/` (which is in the CI integration-test package set) rather
than `internal/api/adminauditlog/` (which is NOT in the CI integration
set as of 2026-05-20 — slice 124's integration tests are local-only).

---

## D-180-5. Why explicit `subject_module='core'` on every existing INSERT call site (AC-5)

**Decision.** Every existing INSERT call site for the nine audit-log tables
now writes `subject_module='core'` explicitly, even though the DB-level
DEFAULT `'core'` would land the same value.

**Why.** Two reasons:

1. **Readability at the call site.** A grep for `INSERT INTO <table>_audit_log`
   surfaces the `subject_module='core'` token immediately; without it,
   reading the call site requires also reading the migration to confirm
   what the DEFAULT is.
2. **Defense-in-depth.** A future migration that drops the DEFAULT — or
   changes it to a different value — does not silently mutate the meaning
   of every legacy write. Explicit writes are immune to DEFAULT drift.

The cost: a one-token diff at each call site. Worth it for the
robustness gain.

**Call sites edited in this slice:**

- **sqlc-managed (8):** `internal/db/queries/walkthroughs.sql`,
  `internal/db/queries/audit_samples.sql`, `internal/db/queries/exceptions.sql`,
  `internal/db/queries/aggregation_rules.sql`, `internal/db/queries/me.sql`,
  `internal/db/queries/evidence_ledger.sql`, `internal/db/queries/feature_flags.sql`,
  `internal/db/queries/audit_periods.sql`. The corresponding `dbx/*.sql.go`
  files regenerate automatically via `just sqlc-generate`.
- **Hand-written (1):** `internal/authz/audit.go` (slice 035) — the
  decision-audit-log insert lives in a hand-written `INSERT` statement
  (not sqlc-managed) because it needs explicit transaction control
  for the slice 036 RLS GUC pattern. Updated in-place.

**Sink Entry construction sites (13).** Beyond the DB INSERT sites,
13 places in the codebase construct a `unifiedlog.Entry` to emit to the
slice 126 external audit-log sink (forensic fan-out). The sink HMACs the
canonical JSON serialization of the Entry, so adding a new field to the
struct silently changes the wire shape. Every construction site now sets
`SubjectModule: unifiedlog.SubjectModuleCore` so the sink emission is
deterministic and the existing HMAC-pinning tests continue to pass.

---

## D-180-6. Why no `omitempty` on the new wire field

**Decision.** The `subject_module` JSON tag is `json:"subject_module"`
(not `json:"subject_module,omitempty"`) on both `unifiedlog.Entry` and
`adminauditlog.UnifiedEntry`.

**Why.** The column is `NOT NULL DEFAULT 'core'` — every row has a
non-empty value. `omitempty` would only fire if the value were the empty
string `""`, which the DB constraint prevents. Adding `omitempty`
"just in case" creates a footgun where a future bug that produces an
empty string would silently drop the field from the wire — a debugging
inversion that's strictly worse than the explicit empty string showing
up.

---

## D-180-7. Why `internal/db/` placement for integration tests

**Decision.** The slice 180 integration test lives at
`internal/db/subject_module_integration_test.go` rather than alongside
the slice 124 integration tests at `internal/api/adminauditlog/`.

**Why.** As of 2026-05-20, CI's `tests-integration` job runs only a
fixed subset of integration packages — `./internal/db/...`,
`./internal/api/scfimport/...`, `./internal/api/anchors/...`,
`./internal/api/schemaregistry/...`, `./internal/evidence/ingest/...`,
`./internal/evidence/streambuf/...`, `./internal/artifact/...`,
`./internal/api/artifacts/...`, `./internal/api/policyacks/...`,
`./internal/featureflag/...`, `./internal/api/features/...`,
`./internal/eval/...`, `./internal/api/controlstate/...`,
`./internal/decision/...`, `./internal/api/dashboard/...`,
`./internal/api/risks/...`, `./internal/authz/...`,
`./internal/control/...`.

`./internal/api/adminauditlog/...` is NOT in the CI matrix, so slice 124's
integration tests in that package are local-only. Adding slice 180's tests
there would inherit the local-only fate. `./internal/db/...` IS in the
matrix, and its `withTenantTx` helper is the right primitive for the
tenant-isolation assertions we need. So the test lives there.

**Spillover candidate (not filed).** Adding `./internal/api/adminauditlog/...`
to the CI integration matrix would let the slice 124 + 180 endpoint-level
tests run on every PR. This is a real follow-on (the slice 124 tests are
valuable regression gates) but it is OUT of slice 180's scope — slice 180
is the foundation slice, not a CI-coverage slice. Surfaced here for the
maintainer to weigh against the existing CI bill.

---

## D-180-8. Decision NOT to add a `subject_module` index (P0-180-2)

**Decision.** No index on `subject_module` is added by this slice's
migration.

**Why (the spec already covers this; recording for traceability).** The
current query patterns filter by `(tenant_id, occurred_at)` — slice 124's
nine indexes are the load-bearing ones. The `subject_module` column is a
**projected** column in the canonical Entry shape, not a filter predicate
on any current query. Adding an index now means paying the write cost on
every audit-log INSERT (high-frequency on busy tenants) for zero query
benefit.

**When (if ever).** Privacy v0 design will determine the real query
patterns. If privacy v0 needs `WHERE subject_module = 'privacy'` queries
(e.g., a privacy-specific unified-log view), the index ships in THAT
slice based on `EXPLAIN ANALYZE` of real workload. If privacy v0 doesn't
need it (entirely possible — the privacy module's primary audit consumers
will be privacy-side processing-activity timelines, which filter on
processing-activity-id NOT module-id), no index is ever added.

---

## D-180-9. Why no spillover slices filed

Three potential spillovers surfaced during this slice's authoring and
were NOT filed because they're tracked elsewhere or premature:

1. **Adding `internal/api/adminauditlog/...` to the CI integration
   matrix** (see D-180-7). Real follow-on but out-of-scope for a
   foundation slice. Maintainer's call whether to file.
2. **Implementing the `module:privacy:enabled` feature flag**. The
   pattern is documented in CONTRIBUTING.md "Module isolation discipline".
   The concrete implementation is a privacy v0 task, not a slice 180
   spillover.
3. **Privacy module data-model design**. Documented in canvas OQ #7
   resolution + this slice's narrative. Will be a series of slices
   (DataSubject + ProcessingActivity + DPIA + DSR + DPV-JSON-LD export)
   when privacy v0 fires. Tracking via canvas, not via spillover slices.

---

## Audit-log table enumeration (sanity check at pickup time)

The slice spec listed nine tables. At pickup (2026-05-20, `main` at
`84c2b41`), all nine exist:

| #   | Table                        | Origin slice | Inserted via                            |
| --- | ---------------------------- | ------------ | --------------------------------------- |
| 1   | `decision_audit_log`         | 035          | hand-written: `internal/authz/audit.go` |
| 2   | `evidence_audit_log`         | 013          | sqlc: `InsertEvidenceAuditEntry`        |
| 3   | `exception_audit_log`        | 021          | sqlc: `WriteExceptionAuditLog`          |
| 4   | `sample_audit_log`           | 026          | sqlc: `WriteSampleAuditLog`             |
| 5   | `audit_period_audit_log`     | 028          | sqlc: `WriteAuditPeriodLog`             |
| 6   | `aggregation_rule_audit_log` | 053          | sqlc: `WriteAggregationRuleAuditLog`    |
| 7   | `feature_flag_audit_log`     | 059          | sqlc: `WriteFeatureFlagAuditLog`        |
| 8   | `me_audit_log`               | 108          | sqlc: `InsertMeAuditLog`                |
| 9   | `walkthrough_audit_log`      | 027          | sqlc: `WriteWalkthroughAuditLog`        |

No tenth table surfaced. The migration touches exactly nine
(`P0-180-7` honored).

---

## Anti-criteria honored

| Anti-criterion | Honored? | Note                                                                                                               |
| -------------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| P0-180-1       | yes      | No `privacy` Postgres schema created.                                                                              |
| P0-180-2       | yes      | No index on `subject_module`.                                                                                      |
| P0-180-3       | yes      | No B4 CI lint rule.                                                                                                |
| P0-180-4       | yes      | No privacy primitives (DataSubject / ProcessingActivity / DPIA / etc.).                                            |
| P0-180-5       | yes      | INSERT call sites edited ONLY to add `subject_module='core'`. No drive-by refactoring.                             |
| P0-180-6       | yes      | Migration is `ADD COLUMN IF NOT EXISTS` + reversible `.down.sql`.                                                  |
| P0-180-7       | yes      | Migration touches ONLY the nine audit-log tables.                                                                  |
| P0-180-8       | yes      | Neutral test fixtures only (`seeder`, `key_seed`, `user-unified-test`).                                            |
| P0-180-9       | yes      | Slice 036's four-policy RLS pattern unchanged. Integration test AC-7 explicitly asserts visibility-set continuity. |

---

## Files touched

**New (4):**

- `migrations/sql/20260520020000_audit_log_subject_module.sql`
- `migrations/sql/20260520020000_audit_log_subject_module.down.sql`
- `internal/db/subject_module_integration_test.go`
- `docs/audit-log/180-privacy-module-foundation-decisions.md` (this file)

**Modified — sqlc query sources (9):**

- `internal/db/queries/walkthroughs.sql`
- `internal/db/queries/audit_samples.sql`
- `internal/db/queries/exceptions.sql`
- `internal/db/queries/aggregation_rules.sql`
- `internal/db/queries/me.sql`
- `internal/db/queries/evidence_ledger.sql`
- `internal/db/queries/feature_flags.sql`
- `internal/db/queries/audit_periods.sql`
- `internal/db/queries/unified_audit_log.sql`

**Modified — sqlc generated output (9, regenerated automatically):**

- `internal/db/dbx/aggregation_rules.sql.go`
- `internal/db/dbx/audit_periods.sql.go`
- `internal/db/dbx/audit_samples.sql.go`
- `internal/db/dbx/evidence_ledger.sql.go`
- `internal/db/dbx/exceptions.sql.go`
- `internal/db/dbx/feature_flags.sql.go`
- `internal/db/dbx/me.sql.go`
- `internal/db/dbx/walkthroughs.sql.go`
- `internal/db/dbx/unified_audit_log.sql.go`
- `internal/db/dbx/models.go` (struct shapes for the audit-log tables now carry `SubjectModule`)
- `internal/db/dbx/querier.go` (no shape changes; regenerated for consistency)

**Modified — hand-written Go (14):**

- `internal/authz/audit.go` (decision_audit_log INSERT call site)
- `internal/audit/unifiedlog/unifiedlog.go` (Entry struct + Query mapper + `SubjectModuleCore` const)
- `internal/api/adminauditlog/unified.go` (UnifiedEntry wire shape + mapper)
- `internal/audit/audit.go` (sink Entry construction)
- `internal/evidence/ingest/ingest.go` (sink Entry construction)
- `internal/featureflag/store.go` (sink Entry construction)
- `internal/audit/walkthrough/walkthrough.go` (sink Entry construction)
- `internal/audit/period/period.go` (sink Entry construction)
- `internal/api/me/profile.go` (sink Entry construction)
- `internal/api/adminauditlog/export.go` (sink Entry construction)
- `internal/decision/store.go` (sink Entry construction)
- `internal/decision/overdue.go` (sink Entry construction)
- `internal/exception/store.go` (sink Entry construction)
- `internal/risk/aggrule/store.go` (sink Entry construction)

**Modified — test files (2):**

- `internal/audit/sink/sink_test.go` (synthetic Entry construction)
- `internal/audit/sink/integration_test.go` (synthetic Entry construction)

**Modified — configuration (1):**

- `sqlc.yaml` (registered the new migration)

**Modified — documentation (3):**

- `CONTRIBUTING.md` (new "Module isolation discipline" section)
- `Plans/canvas/08-audit-workflow.md` (§8.2 OSCAL-doesn't-cover-privacy note)
- `CHANGELOG.md` (Unreleased / Added entry)

---

## Provenance

Authored 2026-05-20 by Claude Opus 4.7 in response to slice 180's pickup
from batch 80. Maintainer (Matt Goodrich) reviews + iterates post-merge
per the slice-development JUDGMENT convention. No human sign-off gate
on the merge itself.
