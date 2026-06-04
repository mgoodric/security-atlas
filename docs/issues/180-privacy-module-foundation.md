# 180 — Privacy-module foundation (audit-log `subject_module` + sibling discipline docs)

**Cluster:** Infra / Multi-tenancy
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Canvas open question #7 resolved 2026-05-20: privacy (GDPR / CCPA / state-level US privacy laws) ships as a **sibling module** living in its own Postgres schema namespace (`privacy.*`) but sharing the platform's auth + tenancy + RLS + audit-log + evidence-citation infrastructure. The actual privacy module (DataSubject / ProcessingActivity / DPIA / DataSubjectRequest primitives) is deferred to v2+ — fires when a real prospect surfaces demand. But several **foundational pre-commitments** need to land NOW, not WITH the privacy module, because they're cheap to add today and expensive to retrofit later.

This slice ships those pre-commitments. After this slice merges, the privacy v0 slice (when it fires) drops in cleanly against a sibling-shaped foundation.

**WHAT this slice ships.**

1. **Migration:** add `subject_module TEXT NOT NULL DEFAULT 'core'` column to each of the nine platform audit-log tables (`decision_audit_log`, `evidence_audit_log`, `exception_audit_log`, `sample_audit_log`, `audit_period_audit_log`, `aggregation_rule_audit_log`, `feature_flag_audit_log`, `me_audit_log`, `walkthrough_audit_log`). All existing rows backfill to `'core'` via the default; future writes can set `'privacy'` (or other future modules) explicitly.
2. **CONTRIBUTING.md update:** new "Module isolation discipline" section codifying the four B1-B4 sub-decisions from the OQ #7 resolution.
3. **Canvas update:** record the OSCAL-doesn't-cover-privacy constraint in `Plans/canvas/04-evidence-engine.md` (or `08-audit-workflow.md` — whichever is the OSCAL-anchor section) so the eventual privacy-v0 export work doesn't accidentally try to abuse OSCAL.
4. **Feature-flag module-toggling pattern doc:** add a short reference in `CONTRIBUTING.md` "Module isolation discipline" section showing the `module:<name>:enabled` flag pattern (used by the future `module:privacy:enabled` toggle).
5. **No actual privacy primitives.** No `privacy` Postgres schema namespace created yet. No data_subjects / processing_activities / dpias tables. No frontend. The foundation is INFRASTRUCTURE-ONLY.

**SCOPE DISCIPLINE — what's deliberately out.**

- **The `privacy` schema namespace itself.** Creating an empty Postgres schema feels tidy but is confusing — better to create it when there's something to put in it (privacy v0). Foundation slice records the convention; privacy v0 creates the namespace.
- **The CI lint rule for B4.** The lint rule fails any PR touching both `internal/api/privacy/**` and `internal/api/controls/**`. With no `internal/api/privacy/` directory existing yet, the rule has nothing to enforce. Lint rule lands WITH privacy v0 (alongside the first `internal/api/privacy/` files).
- **Slice 124 coordination.** Slice 124 (unified audit-log aggregation) is currently `ready` but not merged. If 124 lands before 180, slice 180 updates 124's UNION query to project `subject_module` through. If 180 lands first, 124's UNION naturally picks up the new column. The slice doc records both paths.
- **Privacy primitives themselves** (DataSubject, ProcessingActivity, DPIA, etc.) — that's privacy v0, gated on a real prospect surfacing demand.
- **W3C Data Privacy Vocabulary (DPV) JSON-LD export** — privacy v0 work.
- **Breach-notification workflow** — privacy v0 work; preliminary shape recorded in the OQ #7 resolution block but not implemented here.

## Threat model

Pure infrastructure slice — minimal new threat surface.

**S — Spoofing.** None. No new authenticated endpoints.

**T — Tampering.** The `subject_module` column adds a string field to audit-log writes. Could a malicious caller forge `subject_module='privacy'` on a security event to obfuscate the trail? Mitigation: `subject_module` is set by the WRITING code path (each audit-log INSERT call site), not by user input. The default (`'core'`) catches code paths that don't explicitly set it — which is structurally safer than relying on every call site to opt in. AC-3 asserts every existing audit-log INSERT call site continues to write `'core'`.

**R — Repudiation.** None new. Audit-log integrity invariants from slice 036 (append-only four-policy RLS) are unchanged.

**I — Information disclosure.** None new. RLS policies are unchanged; the new column inherits the same tenant isolation. AC-7 asserts the new column doesn't change the visibility-set under RLS.

**D — Denial of service.** Migration touches 9 tables. Without an index on the new column, queries filtering by `subject_module` could be slow on large tables. Mitigation: NO index on `subject_module` in this slice (premature optimization — current usage filters by `tenant_id + occurred_at` per slice 124's AC-12 indexes; `subject_module` is a projected column, not a filter). When privacy v0 ships, if the unified audit-log query needs to filter by module, add the index in THAT slice based on real query patterns.

**E — Elevation of privilege.** None. The column doesn't gate authorization; it labels rows for module-attribution.

**Verdict.** **has-mitigations** — primary safety property is that `subject_module` is set by the WRITING code path (not user input) and defaults to `'core'` (so legacy code paths can't accidentally label their writes `'privacy'`).

## Acceptance criteria

### Migration

- **AC-1.** NEW migration `migrations/sql/<NEXT_TS>_audit_log_subject_module.sql` adds `subject_module TEXT NOT NULL DEFAULT 'core'` column to **all nine** platform audit-log tables: `decision_audit_log`, `evidence_audit_log`, `exception_audit_log`, `sample_audit_log`, `audit_period_audit_log`, `aggregation_rule_audit_log`, `feature_flag_audit_log`, `me_audit_log`, `walkthrough_audit_log`.
- **AC-2.** Migration is idempotent (uses `ADD COLUMN IF NOT EXISTS` pattern) and reversible (companion `.down.sql` drops the column from all nine tables).
- **AC-3.** Existing rows backfill to `'core'` via the DEFAULT (no separate UPDATE needed). Integration test asserts: after migration, every pre-migration row has `subject_module='core'`.

### sqlc query regeneration

- **AC-4.** `just sqlc-generate` produces an up-to-date sqlc output reflecting the new column on the nine audit-log tables. CI's `Go · sqlc generate diff` check passes.
- **AC-5.** Every existing INSERT call site for the nine audit-log tables continues to write `'core'` (explicit, defense-in-depth — the DEFAULT also handles it but explicit-is-clearer). Code grep: `grep -rE "INSERT.*audit_log" internal/` shows `subject_module='core'` on every match, OR the matching sqlc query uses the DEFAULT.

### Tests

- **AC-6.** Integration test: insert a row into each of the nine audit-log tables; assert `subject_module='core'` is present.
- **AC-7.** Integration test: RLS visibility-set under tenant context is unchanged by the new column (Tenant A still sees only Tenant A's rows; the new column doesn't leak through RLS).
- **AC-8.** Slice 124 coordination test (conditional): if slice 124 is merged before slice 180, this slice's PR updates slice 124's UNION query to project `subject_module` through; integration test asserts the unified-log endpoint surfaces the column. If slice 124 hasn't merged, this slice records the column addition in its decisions log so slice 124's engineer picks up the column naturally in their UNION.

### Documentation

- **AC-9.** NEW section in `CONTRIBUTING.md`: "Module isolation discipline" covering the four sub-decisions from OQ #7 resolution: (B1) Postgres schema isolation; (B2) shared infrastructure; (B3) cross-module reference seam (privacy → evidence + policy, NOT privacy → controls); (B4) lint-rule enforcement (placeholder; rule itself ships with privacy v0).
- **AC-10.** Feature-flag module-toggling pattern documented in the same CONTRIBUTING.md section: `module:<name>:enabled` flag pattern; example `module:privacy:enabled` for the future privacy module.
- **AC-11.** Canvas update: `Plans/canvas/04-evidence-engine.md` (or `08-audit-workflow.md`, whichever is the OSCAL anchor section) gets a note: "OSCAL covers security primitives only. Privacy module exports (when privacy v0 ships) use W3C Data Privacy Vocabulary as JSON-LD — NOT OSCAL." Engineer picks the most appropriate section at pickup.
- **AC-12.** CHANGELOG entry under `[Unreleased] / Added`: "`subject_module` column on all platform audit-log tables (pre-commitment for the deferred privacy sibling module; #180)."

### Audit log + decisions log

- **AC-13.** Decisions log at `docs/audit-log/180-privacy-module-foundation-decisions.md` records: (a) the OQ #7 resolution shape and rationale (link to canvas); (b) why no `privacy` Postgres schema is created yet (premature; lands with privacy v0); (c) why no lint rule is added yet (nothing to enforce until `internal/api/privacy/` exists); (d) slice 124 coordination outcome (which path was taken — 180-before-124 or 124-before-180).

## Constitutional invariants honored

- **Invariant #6 — tenant isolation via RLS.** The new column does not bypass RLS; AC-7 asserts the visibility-set is unchanged.
- **Invariant #2 — append-only evidence ledger.** Audit-log tables remain append-only (slice 036 four-policy RLS pattern); this slice ADDS a column but does not change the immutability invariant.
- **OQ #7 sibling-module decision** — this slice is the foundation work that makes the sibling shape land cleanly when privacy v0 fires.

## Canvas references

- `Plans/canvas/11-open-questions.md` #7 (resolved 2026-05-20) — sibling module decision + four sub-decisions
- `Plans/canvas/04-evidence-engine.md` — OSCAL wire format coverage
- `Plans/canvas/08-audit-workflow.md` — audit-log lifecycle

## Dependencies

- **#036** (Append-only audit-log four-policy RLS) — `merged`. The pattern this slice extends.
- **#124** (Unified audit-log aggregation) — currently `ready`, not merged. Slice 180 coordinates with 124 (either order is workable; AC-8 covers both paths).
- All nine audit-log table slices — `merged` (since their respective domain slices shipped pre-v1).

## Anti-criteria (P0 — block merge)

- **P0-180-1.** Does NOT create a `privacy` Postgres schema namespace. Empty schemas are confusing; the namespace lands with privacy v0 when there's something to put in it.
- **P0-180-2.** Does NOT add an index on `subject_module`. Premature optimization; current query patterns filter by `tenant_id + occurred_at`. If privacy v0 needs a module-filtered query, the index ships in THAT slice with real workload data.
- **P0-180-3.** Does NOT add the B4 CI lint rule (`internal/api/privacy/**` ↔ `internal/api/controls/**` cross-module guard). Rule lands with privacy v0 alongside the first `internal/api/privacy/` files. Adding the rule now means it lints nothing.
- **P0-180-4.** Does NOT introduce ANY privacy primitives (DataSubject, ProcessingActivity, DPIA, DataSubjectRequest, lawful_basis, etc.). Foundation slice is INFRASTRUCTURE-ONLY. Adding even one primitive to "get started" violates the sibling-shape discipline because primitives belong in the namespace this slice deliberately doesn't create.
- **P0-180-5.** Does NOT modify any existing audit-log INSERT call site beyond adding `subject_module='core'` (or relying on the DEFAULT). No drive-by refactoring.
- **P0-180-6.** Migration MUST be idempotent (`ADD COLUMN IF NOT EXISTS`) and reversible (companion `.down.sql` exists).
- **P0-180-7.** Migration MUST NOT add `subject_module` to non-audit-log tables. Nine tables exactly: those listed in slice 124. Adding the column elsewhere is scope creep.
- **P0-180-8.** Neutral test-fixture tokens only (slice 005 convention).
- **P0-180-9.** Does NOT relax slice 036's four-policy RLS pattern. The new column does not change the policy structure.

## Skill mix (3-5)

1. **Atlas + sqlc migration discipline** — multi-table column addition; pinned `sqlc v1.31.1` (memory note: pinned version per justfile slice 109)
2. **CONTRIBUTING.md authorship** — sibling-discipline doc + feature-flag pattern reference
3. **Postgres RLS continuity testing** — AC-7 asserts the new column doesn't change visibility-set under RLS
4. **Coordination discipline** — AC-8 handles slice 124 ordering both ways

## Notes for the implementing agent

### Slice 124 coordination

Slice 124 is the unified audit-log aggregation API; it UNIONs across the nine tables we're modifying. Both orderings are workable:

- **If 180 lands first** (most likely given slice 124 is solo JUDGMENT 3-4d): slice 124's engineer naturally picks up the `subject_module` column in their UNION ALL projection. Make sure 124's canonical-schema projection includes `subject_module TEXT NOT NULL DEFAULT 'core'`.
- **If 124 lands first** (less likely): slice 180 updates 124's UNION query in its PR. Update the canonical schema docs in 124's package comment.

At pickup time, the engineer checks the merge state of slice 124 via `gh pr list --search "in:title slice 124"` and records the coordination outcome in the decisions log (AC-13).

### Audit-log table enumeration

The nine tables are (verbatim from slice 124's narrative):

1. `decision_audit_log`
2. `evidence_audit_log`
3. `exception_audit_log`
4. `sample_audit_log`
5. `audit_period_audit_log`
6. `aggregation_rule_audit_log`
7. `feature_flag_audit_log`
8. `me_audit_log`
9. `walkthrough_audit_log`

If a tenth audit-log table has landed between slice 124's filing and slice 180's pickup, the engineer adds it to the migration AND records the addition in the decisions log.

### Feature-flag pattern (AC-10 reference shape)

The `module:<name>:enabled` flag pattern is documentation-only in this slice. Concrete shape for the eventual privacy-v0 implementation:

```
Flag name:  module:privacy:enabled
Type:       boolean
Scope:      per-tenant (default false)
Default:    false (privacy module surfaces hidden until tenant opts in)
Set via:    admin endpoint POST /v1/admin/tenants/:id/flags
```

This slice does NOT need to implement the flag; just document the convention in CONTRIBUTING.md so privacy v0's engineer follows the pattern.

### Spillover candidates

If during this slice an out-of-scope finding emerges:

- **A tenth audit-log table** discovered during migration authoring — add it to scope of this slice (not a spillover); doesn't violate scope discipline since the slice's premise is "all platform audit-log tables get the column."
- **Existing audit-log INSERT call site missing `subject_module='core'`** — fix as part of this slice (AC-5).
- **Privacy v0 design conversation surfacing** — file as a SEPARATE design-grill slice; this slice is foundation-only.
- **Module-isolation lint rule premature design** — file as separate slice gated on `internal/api/privacy/` existing.

### Provenance

Filed 2026-05-20 as the foundation slice for the OQ #7 resolution. Maintainer accepted Option B (sibling module) + all four sub-decisions (B1-B4) + explicitly asked for the pre-commitment work to land as a slice so the eventual privacy v0 lands cleanly against a sibling-shaped foundation.
