# Decisions log — #1765: demo seed into the sole tenant

- detection_tier_actual: integration
- detection_tier_target: integration

Ticket: security-atlas#1765 (spec #1758, user stories 28-29). On a
single-tenant install, `atlas-cli demo seed` now loads the demo dataset into
the install's only tenant, provided it holds nothing beyond the stock
bootstrap seed. `demo teardown` removes exactly the rows it added. Creating a
separate demo tenant beside an existing one needs `ATLAS_ALLOW_MULTI_TENANT=true`.
The bug this work surfaced (below, D4) was caught by the new integration tests
on their first run.

## D1 — `--tenant-slug` decides the mode

There's no mode flag. Omitting `--tenant-slug` means "my organisation":
`tenancy.ResolveImplicitTenant` picks the only tenant, and the seed runs in
place. Passing it means "a separate demo tenant", today's behaviour. A slug
that already names a tenant still goes to `Apply`, which re-runs
idempotently or refuses as before. This matches #1762/#1988, where an omitted
tenant flag means the install's only tenant and errors with a tenant list
when there are several.

## D2 — "Stock" is defined by the bootstrap's own sources

The in-place seed refuses (`ErrTenantHasOrgData`) when the tenant has any of:
evidence, risks, tenant-private framework versions, controls whose
`bundle_id` is not one of the 50 `controls/soc2/` bundles `bootstrap.sh`
uploads, or policies whose `created_by` is not the stock policy seed's
`system:stock-seed`.

- The stock bundle IDs are a Go list (`internal/demoseed/stock.go`), because
  the CLI binary can't rely on `controls/soc2/` being on disk.
  `TestStockControlBundleIDs_MatchControlsDir` fails if the list and the
  directory drift.
- Tenant-private framework versions are refused although the ticket names
  only controls, risks, evidence and policies. The posture spine
  (`writeFrameworkPostureSpine`) hangs requirements, anchors and edges off a
  tenant `current` framework version. Those child tables have no
  `tenant_id`, so a real version adopted by the seed would carry
  unrecorded demo children. A fresh install has none, so refusing costs
  nothing there. This is a stricter refusal than the ticket asks for.
- A stock control re-uploaded with edits is still stock: it is identified by
  `bundle_id`, which is how the bootstrap's own re-upload identifies it
  (#1909).

## D3 — Recording rows by snapshot diff, in a `demo_seed_rows` table

Migration `20260924000000_demo_seed_rows.sql` adds `demo_seed_rows
(tenant_id, table_name, row_key jsonb)`, RLS-enforced like any tenant table.
Inside the seed transaction, `ApplyInPlace` reads the primary key of every
row in every table with a `tenant_id` column (found from `pg_catalog`; the
whole row when there is no primary key). It does this before and after the
writers run, and records the difference. The after-snapshot keeps only rows
this transaction wrote (`xmin` equals its transaction id). The seed runs at
READ COMMITTED, so without that filter a row another session committed for
the tenant mid-seed would be recorded and later deleted by teardown (#2039
review B1; `TestSeedInPlace_RowCommittedMidSeedBySomeoneElse_IsKept`).

The alternative was to have each of the ~30 writers record its own rows.
That breaks silently the next time someone adds a writer and forgets to
record. Discovering the tables means a new writer, or a trigger-written
audit row, is covered without anyone remembering to.

Teardown deletes the recorded keys with `to_jsonb(t.*) @> ANY(keys)`. It goes
in `teardownTables` order, then alphabetically, retrying a table whose
delete a foreign key from a not-yet-deleted demo row blocks (each try in a
savepoint).

## D4 — The #1830 evidence guard recognises the in-place context

`assertFreshDemoTenant` (#1830) confines the demo seed's backdated raw
evidence insert to a tenant created in the same transaction. In-place
seeding hit it on first run. The guard now also passes when the
transaction:

- wrote a `demo_seed_apply` mark carrying `in_place: true` and this tenant's
  id; and
- finds no evidence in the tenant other than its own.

So the backdated insert cannot reach a tenant that held real evidence when
the guard ran, which is #1830's stated property.

A window remains. `evidence_records` has no foreign key to `tenants`, so the
`FOR UPDATE` lock on the tenant row does not block another session's ingest.
Evidence pushed for the tenant after the guard runs, while the seed
transaction is still open, lands beside the demo evidence. It is never
recorded (B1 above), so teardown keeps it. But for that stretch the tenant
holds real and demo evidence together. Closing the window would need a
lock that ingest also takes; ingest takes none today. On a fresh install
nothing is pushing evidence while an operator runs `demo seed`, so the
window is accepted, not closed. The guard tests cover the new arm passing,
a mark naming another tenant, and a tenant with earlier evidence. The
in-place mark carries the tenant id because the bootstrap tenant has no slug
to match on.

## D5 — Teardown never drops a tenant seeded in place, and leaves a trace

`Teardown(slug)` and the new `TeardownTenant(id)` check `demo_seed_rows`
first. A tenant seeded in place also carries the `demo_seed_apply` mark, and
without this check `demo teardown --tenant-slug=<your org>` would have
deleted the whole organisation.

Audit logs keep a trace (Matt's decision M2, below). The seed never records
rows in the tenant's audit-log tables (`*_audit_log`, `*_audit`,
`isTenantAuditTable`), so teardown never deletes them. That includes the
`demo_seed_apply` mark and the ~50 synthetic `me_audit_log` rows; every one
carries `demo_seed_v`, so the filter in `demo-seed.md` separates them from
real activity. Teardown adds its own `demo_seed_teardown` row to
`me_audit_log` and `super_admin_audit_log`, with the actor, the total
`removed_rows` and per-table `removed` counts. AC-2's "exactly the pre-seed
rows" therefore holds for every table except the audit logs.
`TestSeedInPlace_Teardown_LeavesExactlyPreSeedRows` asserts both halves:
nothing outside the audit logs differs from before the seed, and the tenant's
`me_audit_log` shows the apply and then the teardown.

Because those marks now outlive teardown in a real organisation,
`tenantWasSeeded`, the test for "this is a whole demo tenant", ignores marks
carrying `in_place`. Without that, a second `demo teardown
--tenant-slug=<your org>` after an in-place teardown would find the mark,
take the whole-tenant path and delete the organisation.
`TestSeedInPlace_AfterTeardown_SlugTeardownNeverDropsTheTenant` fails without
the filter: the tenant is deleted.

## Matt's decisions on the #2039 review (2026-11-14)

- **M1 — hard-delete from append-only ledgers: yes.** Teardown may
  hard-delete the recorded demo rows from append-only ledgers
  (`evidence_records` and similar) of a live tenant. Only recorded rows are
  deleted, and since the B1 fix (D3) a recorded row is provably one the seed
  transaction wrote.
- **M2 — keep a trace.** Audit-log rows are never recorded as demo rows, so
  teardown never deletes them. Teardown writes its own `me_audit_log`
  `demo_seed_teardown` row with the actor and counts. AC-2 excludes the
  audit log. Implemented as D5.

## D6 — Rows added after the seed follow the schema's delete rules

Teardown deletes only recorded rows, so it never removes a row the seed did
not add directly. A row added later that a foreign key ties to a demo row
follows that key's delete rule:

- `ON DELETE CASCADE` rows go with the demo row, as they would in a
  whole-tenant teardown. For example, an owner assigned to a demo control.
- `ON DELETE RESTRICT` rows make teardown refuse and name the table. For
  example, an exception filed against a demo control.

Refusing on every cascade would need a full dependency walk, for little gain
over the documented rule. Tested by
`TestSeedInPlace_Teardown_RefusesWhenRealRowReferencesDemoRow`.

## D7 — Post-seed evaluation still runs over the whole tenant

The CLI's evaluation pass (slice 671) evaluates every active control, the
stock ones included, and writes derived read-model rows (`control_evaluations`,
`evidence_freshness`) after the seed transaction committed. They are not
recorded. Demo controls' rows cascade away with the controls. Stock controls'
rows stay; the scheduled evaluator would write the same rows on its next
run. AC-2 is asserted at the seeder layer, which is where "exactly the
pre-seed rows" is a property of this code.

## D8 — The multi-tenant gate lives in `Seeder.Seed`, used by the CLI

`Seed` refuses a new `--tenant-slug` when another tenant exists and
`AllowMultiTenant` is false. The CLI passes
`admintenants.DefaultAllowMultiTenant()`. The super-admin HTTP demo button
(`internal/api/admindemo`) still calls `Apply` directly and creates a `demo`
tenant as before. That button is the subject of other #1758 work and is out
of this ticket's CLI scope; it is filed as #2043 rather than changed here.

## D9 — Tests stand in for "the only tenant" with a resolver override

The shared integration database always holds many tenants, so the tests
inject the resolver's single-tenant answer with `WithTenantResolver`. "Tenant
count stays 1" is asserted as "the tenant count does not change".
