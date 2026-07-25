# Slice 659 — OSCAL component-definitions list 500: decisions log

**Slice type:** JUDGMENT (DIAGNOSE-THEN-DECIDE — reproduce first, then fix-or-diagnose).
**Conclusion (one line):** the list code is CORRECT on a fully-migrated DB; the edge 500 is
**migration-lag** (edge `imported_catalogs` missing the `kind`/`profile_title` columns from
migration `20260608000000`). No code fix; regression guard added + deploy-note spillover (683) filed.

- detection_tier_actual: production
- detection_tier_target: integration

(`actual=production`: the 500 surfaced on the edge deploy. `target=integration`: a migrated-DB
integration test asserting the empty-tenant list returns 200 would have caught a genuine
query/RLS bug in CI; the migration-lag itself is `target=integration` for a migrate-on-bringup
canary — a clean-bring-up smoke that the slice-589 read endpoints answer 200 would catch the
edge DB being short a migration before a user does. The deploy/migrate-observability axis is
slice 683 AC-4 / slice 659 AC-2.)

---

## D1 — Reproduce result (the load-bearing step)

Stood up a fresh Postgres 16, applied `migrations/bootstrap/01-roles.sql` + **all 83**
`migrations/sql/*.sql` in order (clean apply, no failures), set `atlas_app` NOBYPASSRLS so RLS
is actually enforced, and ran the package integration suite against it.

**On the fully-migrated DB the 500 does NOT reproduce:**

- **Empty tenant** → `GET /v1/oscal/component-definitions` returns **HTTP 200** with the exact
  symptom-page wire shape `{"component_definitions":[],"count":0}`. Verified both at the SQL
  level (the literal generated query returns 0 rows cleanly) and through the assembled router
  via the new test `TestList_EmptyTenantReturns200EmptyList`.
- **Populated tenant** (one imported component-definition + one component + two claims, seeded
  exactly as the slice-512 importer writes — `kind='component_definition'`, `is_vendor_claim=TRUE`,
  `claim_status='asserted'`) → **HTTP 200** with the row (`TestList_ReturnsDefinitions`).
- **RLS** → tenant B's list does not include tenant A's definition; a cross-tenant detail GET
  404s (`TestRLS_CrossTenantIsolation`). Invariant #6 holds.

Full package suite (16 tests incl. the new guard): **PASS** on the migrated DB.

**Root cause of the edge 500 (proven at the SQL level):** sqlc expands the `SELECT *` in
`internal/db/queries/imported_catalogs.sql` (`ListImportedComponentDefinitions`) into an explicit
column list AND the query carries `WHERE kind = 'component_definition'`. The generated const string
references `kind` and `profile_title` — columns added by `20260608000000_oscal_imported_profiles.sql`
(slice 511). Against an edge-shaped `imported_catalogs` lacking those columns, Postgres rejects the
query at **parse time**: `ERROR: column "kind" does not exist` — **regardless of row count**, which is
exactly why the EMPTY/default tenant 500s. Demonstrated by running the literal generated query
against a `LIKE imported_catalogs` table with `kind`/`profile_title` dropped:

```
ERROR:  column "kind" does not exist
HINT:  Perhaps you meant to reference the column "edge_sim.id".
```

## D2 — Conclusion: migration-lag, not a code bug

The platform binary on edge is **ahead of the edge DB schema** (the slice-473 binary-ahead-of-schema
migration-lag pattern). The edge `imported_catalogs` table is missing `kind`/`profile_title` from
`20260608000000`. Because the orchestrator re-test still 500'd after a redeploy, the operative
question is WHY `atlas-migrate-edge` did not apply that migration on the last `up` — but that is an
edge-deployment fact (logs + DB state), not something the repo can settle or fix. No platform code
or migration is changed by this slice.

**No query/schema mismatch found in the code itself.** `git diff --stat origin/main...HEAD -- proto/`
and `-- migrations/` are both EMPTY. No sqlc regen. The only code artifact is the new integration test.

## D3 — Migration-lag spillover + why it's not a code fix

Filed `docs/issues/683-oscal-edge-migration-lag-deploy.md` (next free slice; 682 was highest) and
registered its row in `_STATUS.md` as `not-ready` (BLOCKED on maintainer edge-log access). It cannot
be code: slice 659 proved the query/handler/RLS are correct on a migrated DB; the fix is operational
(apply `20260608000000` + the dependent `20260608010000` on the edge box, and read the
`atlas-migrate-edge` logs to find why migrate-on-bringup skipped/failed). "Fixing" this in code by
weakening the query or catch-and-empty-stating a parse error would mask the next genuine schema drift —
explicitly an anti-criterion (slice 659 AC anti-criteria).

## D4 — No internal-error-leak verification

The handler's 500 path is `httperr.WriteInternal(w, r, "oscalcomponents", err)` (slice 367). That
helper writes ONLY `{"error":"internal error","request_id":"<id>"}` — no `err.Error()` reflection, no
table/column/constraint names, no SQLSTATE, no stack. The slice-367 contract test
`TestWriteInternal_GenericClientBody` already locks this (asserts the body never contains the pgx
text/constraint/SQLSTATE). So even on the lagged edge DB the `column "kind" does not exist` message
stays server-side (logged with the request id for triage). No change needed; verified by reading the
call site + the httperr package contract.

**FE note (not in scope — flagged per the prompt):** slice 659's own slice doc AC-4 wants the page to
render a clean empty state rather than the red "Could not load…" banner. That banner only appears
because the FE receives a 500 from the lagged edge — once the edge migration lands (slice 683), the
empty tenant returns 200 + an empty list and the page renders cleanly. A FE hardening to also degrade
gracefully on a 5xx (show empty state, not a red banner) is a separate FE concern; not expanded here.

## Coverage

New integration test `TestList_EmptyTenantReturns200EmptyList` in
`internal/api/oscalcomponents/integration_test.go` (`//go:build integration`). The package has no
Go-unit coverage floor entry (it is an integration-tier handler package; its existing coverage is the
`integration_test.go` suite). The new guard is additive to that suite — no unit floor is lifted, so no
ratchet move is required. Full package integration suite green on a fresh-migrated DB.

## Files

- `internal/api/oscalcomponents/integration_test.go` — added `TestList_EmptyTenantReturns200EmptyList`.
- `docs/issues/683-oscal-edge-migration-lag-deploy.md` — deploy-note spillover (operational).
- `docs/issues/_STATUS.md` — 659 row → `in-review` (PR ref at row end); 683 spillover row registered `not-ready`.
- `CHANGELOG.md` — `### Changed` note (regression-test-only; no behavior change).
