# 683 — OSCAL component-definitions edge migration-lag (deploy-note / operational)

**Cluster:** Deploy / OSCAL
**Estimate:** XS (operational; no code change)
**Type:** OPERATIONAL (maintainer edge access required)
**Status:** `not-ready` — BLOCKED on maintainer edge-log access (cannot diagnose/fix the edge DB from code).
**Spillover from:** slice 659 (the migration-lag conclusion of the Vendor Claims list-500 reproduce).

## Narrative

Slice 659 reproduced the `/oscal/component-definitions` list-500 on a **fully-migrated**
Postgres and found the platform code is correct: an empty tenant returns
`200 {"component_definitions":[],"count":0}`, a populated tenant returns the rows, and
cross-tenant reads are RLS-isolated. The integration test
`TestList_EmptyTenantReturns200EmptyList` (slice 659) locks that in.

The edge 500 is therefore **migration-lag**: the edge `imported_catalogs` table is missing
the `kind` and `profile_title` columns added by migration
`migrations/sql/20260608000000_oscal_imported_profiles.sql` (slice 511). The generated
`ListImportedComponentDefinitions` query references `kind` in BOTH the `SELECT` list and the
`WHERE kind = 'component_definition'` predicate, so Postgres rejects it at **parse time** with
`column "kind" does not exist` — **regardless of row count** (which is why even the EMPTY tenant
500s). The handler maps the store error to a generic 500 via `httperr.WriteInternal` (no
internal detail leaks — verified in slice 659 D4).

This is an **operational** fix, not a code change: the platform binary on edge is ahead of the
edge DB schema (the slice-473 "binary-ahead-of-schema migration-lag" pattern). The maintainer
must determine why `atlas-migrate-edge` did not apply `20260608000000` (and the dependent
`20260608010000_oscal_component_definitions.sql` for the detail/disposition path) on the last
`up`.

## Acceptance criteria (operational — needs maintainer edge access)

- [ ] **AC-1.** Capture the `atlas-migrate-edge` job logs from the last edge `up`. Determine
      whether migration `20260608000000_oscal_imported_profiles.sql` was applied, skipped, or
      failed fail-closed (a halted chain leaves later migrations including
      `20260608010000_oscal_component_definitions.sql` unapplied too).
- [ ] **AC-2.** Inspect the edge DB:
      `SELECT column_name FROM information_schema.columns WHERE table_name='imported_catalogs';`
      Confirm `kind` + `profile_title` are absent (the predicted state). Check the
      migration-tracking table / `\dt` for the slice-511/512 tables.
- [ ] **AC-3.** Apply the missing migration(s) on the edge box (re-run `atlas-migrate-edge` /
      `just migrate-up` against the edge DSN), then re-test
      `GET /api/oscal/component-definitions` returns 200 in the EMPTY/default tenant.
- [ ] **AC-4.** Root-cause WHY migrate-on-bringup did not apply it on the prior `up` (image
      tag lag? a prior migration failed and halted the chain? the migrate step was skipped?).
      If it is a deploy-robustness gap (migrate failures silently not halting the deploy), file
      a follow-on slice for the deploy/migrate observability axis (slice 659 AC-2 scope).

## Anti-criteria

- This slice does NOT change platform code — slice 659 already proved the query/handler/RLS are
  correct on a migrated DB. Do NOT "fix" this by weakening the query or catching-and-empty-stating
  a real parse error (that would mask the next genuine schema drift).
- Does NOT widen the OSCAL read surface or change tenant scoping (RLS stays).

## Dependencies

- Slice 659 (the reproduce + regression guard) — establishes that the code is correct and the
  cause is migration-lag.
- Requires the maintainer's edge-deployment access (logs + DB) — cannot proceed from the repo.

## Notes

Reference: slice 659 decisions-log `docs/audit-log/659-oscal-component-definitions-500-decisions.md`
(D1 reproduce, D2 migration-lag conclusion, D3 this spillover). Pairs with the slice-473
migration-lag pattern and slice 659 AC-2 (deploy/migrate robustness).
