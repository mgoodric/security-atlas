# 683 — OSCAL component-definitions edge migration-lag (deploy-note / operational)

**Cluster:** Deploy / OSCAL
**Estimate:** XS (operational; no code change)
**Type:** OPERATIONAL (maintainer edge access required)
**Status:** `retired` — target decommissioned; no edge deployment or edge DB remains to diagnose.
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

## Retirement confirmation (OE-419, 2026-07-25)

This slice is retired rather than investigated. The target it described no longer exists.

- **Edge absence confirmed:** OE-202 (`Atlas residue cleanup`, Plane sequence 202) is closed
  (`completed_at=2026-07-21T16:02:14Z`) and records that the security-atlas platform
  "prod+edge+watchers" was removed before 2026-07-02. Its cleanup scope includes the
  consumer-less `security_atlas` database plus the orphaned
  `/mnt/user/appdata/security-atlas/`, `/mnt/user/appdata/atlas-edge/`,
  `/mnt/user/appdata/atlas-startup-watcher/`, and
  `/mnt/user/appdata/atlas-edge-startup-watcher/` directories. The staged OE-202 evidence
  in `/Users/gmoney/Development/open-engine/staged/oe202-atlas-residue-cleanup/README.md`
  separately records `security_atlas` as consumer-less and `atlas-edge` appdata as orphaned
  with no container mounts. The current canonical infra tree
  `/Users/gmoney/Development/open-engine/gmoney-apps/` has no `security-atlas`,
  `atlas-edge`, or `security_atlas` references.
- **Code-side guarantee confirmed:** slice 659's regression guard remains present at
  `internal/api/oscalcomponents/integration_test.go`:
  `TestList_EmptyTenantReturns200EmptyList`. Verified passing with
  `go test -tags=integration ./internal/api/oscalcomponents -run '^TestList_EmptyTenantReturns200EmptyList$' -count=1`.
- **Deploy-time migration-lag guard confirmed present:** slice 473's always-run
  `atlas-migrate` / `atlas-migrate-edge` path is still present. `deploy/docker/bootstrap/migrate.sh`
  applies unapplied `migrations/sql/*.sql` entries through the `schema_migrations` ledger,
  exits non-zero on the first failed migration, and logs that the backend must not serve
  a partial schema. `deploy/docker/docker-compose.edge.yml` labels `atlas-migrate-edge`
  for Watchtower lockstep updates and gates `atlas-edge` on
  `atlas-migrate-edge: condition: service_completed_successfully`. Because this guard already
  exists, no follow-up OE is filed from this retirement slice.

No platform code change is made here. The original edge DB cannot be inspected or migrated
because OE-202 removed that environment; the durable value is the surviving regression test plus
the existing slice-473 fail-closed deploy guard.

## Acceptance criteria (operational — needs maintainer edge access)

- [x] **AC-1 (retired).** Capture the `atlas-migrate-edge` job logs from the last edge `up`. Determine
      whether migration `20260608000000_oscal_imported_profiles.sql` was applied, skipped, or
      failed fail-closed (a halted chain leaves later migrations including
      `20260608010000_oscal_component_definitions.sql` unapplied too). **Superseded by
      OE-202:** the edge deployment and DB were decommissioned before this could be inspected.
- [x] **AC-2 (retired).** Inspect the edge DB:
      `SELECT column_name FROM information_schema.columns WHERE table_name='imported_catalogs';`
      Confirm `kind` + `profile_title` are absent (the predicted state). Check the
      migration-tracking table / `\dt` for the slice-511/512 tables. **Superseded by
      OE-202:** there is no edge DB left to inspect.
- [x] **AC-3 (retired).** Apply the missing migration(s) on the edge box (re-run `atlas-migrate-edge` /
      `just migrate-up` against the edge DSN), then re-test
      `GET /api/oscal/component-definitions` returns 200 in the EMPTY/default tenant.
      **Superseded by OE-202:** no edge DSN exists; slice 659's integration test is the
      surviving empty-tenant guarantee.
- [x] **AC-4.** Root-cause WHY migrate-on-bringup did not apply it on the prior `up` (image
      tag lag? a prior migration failed and halted the chain? the migrate step was skipped?).
      If it is a deploy-robustness gap (migrate failures silently not halting the deploy), file
      a follow-on slice for the deploy/migrate observability axis (slice 659 AC-2 scope).
      **Closed as already guarded:** slice 473 already provides the deploy-time guard via
      always-run `atlas-migrate-edge` and `service_completed_successfully` gating, so no
      follow-up OE is needed.

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
