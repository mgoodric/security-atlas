# Decisions log — #2043: demo button gate, seed-mark anchoring, demo provenance

- detection_tier_actual: integration
- detection_tier_target: integration

Ticket: security-atlas#2043, a follow-up to #2039 (#1765) D8 and its review.

- The super-admin demo button now respects `ATLAS_ALLOW_MULTI_TENANT`.
- The seeder's forensic mark can no longer make a real organisation
  eligible for demo teardown.
- Demo rows that the in-place seed added to an organisation carry a "demo"
  mark in the UI and in every export.

The bug this work fixed (D2) was confirmed by new integration tests, which
failed on `main` before the fix. The organisation was deleted.

## D1 — The button gates, it does not seed in place

The ticket allowed either fix: apply the #1763 gate, or seed the sole tenant
in place the way the CLI does. The button now seeds through `Seeder.Seed`
with the hard-coded `demo` slug and `AllowMultiTenant` read from
`ATLAS_ALLOW_MULTI_TENANT`. Without the opt-in, a click beside an existing
tenant returns 403 with the admintenants message, plus a pointer to
`atlas-cli demo seed` for the in-place path.

In-place seeding from the button was not chosen, for three reasons:

- The button takes no tenant input (P0-278-2).
- Its teardown deletes by slug.
- An in-place seed into the clicking admin's organisation needs its own
  confirmation UX.

That is a separate feature, not a fix.

The click is audited before the refusal (`demo_seed` me_audit_log row), like
the other precondition refusals (409s). The 503 env gate and the 403 role gate
still refuse before the audit write. Teardown is not gated: it never creates
a tenant. Re-seeding an existing `demo` tenant needs no opt-in, because
`Seed` only applies the gate when the slug names no tenant yet.

## D2 — Seed marks: two layers (dispatcher scope added to #2043)

The hazard: `writeAuditLogRow` wrote the `demo_seed_apply` me_audit_log mark
to the actor's tenant, and `tenantWasSeeded` treated any such mark as proof of
a whole demo tenant. A button click on a multi-tenant install therefore marked
the super admin's real organisation, and `demo teardown --tenant-slug=<org>`
deleted it. `TestApply_ActorTenantMark_TeardownBySlugNeverDropsActorOrg`
showed exactly that before the fix.

- **(a) `tenantWasSeeded` matches the slug.** A mark counts only when
  `after->>'slug'` equals the tenant's own slug. This also neutralises
  marks already written into real organisations by earlier clicks: they name
  `demo`, not the organisation (`TestTeardown_LegacyMarkInActorOrg_...`).
  Anchoring alone would have missed those rows.
- **(b) The mark anchors to the demo tenant.** `writeAuditLogRow` now always
  writes the me_audit_log row under the seeded tenant. The actor stays on
  record in super_admin_audit_log (`actor_user_id`, `actor_tenant_id`), and
  admindemo writes its own `demo_seed` row to the actor's tenant.

Side effect of (b): a whole-tenant teardown's `demo_seed_teardown`
me_audit_log row is now written into the tenant it then deletes. It goes with
the tenant, as it already did on the CLI path (nil actor). The
super_admin_audit_log row is the surviving record, which is where
install-wide actions belong. The in-place path is unchanged: its tenant is
the organisation, so its marks stay there (M2).

A bonus fix: before (b), a button-created `demo` tenant held no mark of its
own when the actor had a tenant. So `demo teardown --tenant-slug=demo`
refused it, which surfaced as a cleanup warning in the existing tests.

## D3 — `*_log` tables in the audit-table pattern

`isTenantAuditTable` decides which tables the in-place seed never records and
teardown leaves as a trace (M2). Until now it matched only the `*_audit_log` and
`*_audit` suffixes. The tenant-scoped tables whose names contain `log` but miss
those suffixes:

| Table                          | Decision                      | Why                                                                                                                                         |
| ------------------------------ | ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `framework_scope_workflow_log` | **Audit** (excluded)          | A source of the unified audit log (`internal/audit/unifiedlog`, #1786): who submitted/approved a framework-scope change. A trace, not data. |
| `artifact_access_log`          | **Audit** (excluded)          | Who downloaded which evidence artifact: an access trail. Deleting it on teardown would erase who looked at what.                            |
| `channel_delivery_log`         | Ordinary (recorded if seeded) | A notification send record. If the seed ever caused a send, removing it with the seed is correct.                                           |
| `email_delivery_log`           | Ordinary (recorded if seeded) | Same as `channel_delivery_log`.                                                                                                             |
| `staleness_rollup_log`         | Ordinary (recorded if seeded) | An evaluation read model, derived from the ledger. Like `control_evaluations` (#1765 D7), it is not an audit trail.                         |

None of the five gets rows from the seed today. The decision matters only for
what a future writer would leave behind. Two names that look relevant are not
logs: `imported_catalog_controls` and `imported_catalogs` (matched by the
`log` search only through "catalog") and `metrics_catalog`. All three stay
ordinary. The audit pair is an explicit list (`auditTrailLogTables`), not a
wider suffix: `*_log` would also sweep up the delivery and read-model tables.
The integration tests' `withoutAuditTables` now calls the same predicate, so
the test and the seeder cannot drift.

## D4 — Demo provenance is read from `demo_seed_rows`, per surface

A new package, `internal/demoprov`, does one thing: given a table and a page of
ids, it returns the ones `demo_seed_rows` records. The sqlc query
`DemoSeededIDs` matches whole `{"id": …}` keys, so the
`(tenant_id, table_name, row_key)` unique index serves it. It runs on the
caller's querier inside its tenant transaction, so RLS scopes it like every
other read (`TestDemoProvenance_MarksExactlyTheInPlaceSeedRows` checks that
another tenant sees no marks).

No column was added to `controls` or `evidence_records`. Two reasons:

- `demo_seed_rows` is already the record of truth.
- `evidence_records` is an append-only ledger (invariant #2).

Only rows seeded in place are marked. A separate demo tenant holds nothing
but demo data, so a per-row badge there would add nothing.

## D5 — Where the mark shows

| Surface                                                                                                                     | Mark                                                                             |
| --------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `GET /v1/controls?include=state` → controls list                                                                            | `demo: true` → **Demo** badge next to the name                                   |
| `GET /v1/controls/{id}/coverage` → control page header                                                                      | `control.demo: true` → badge beside the lifecycle badge                          |
| `GET /v1/evidence` (per control and tenant-wide) → evidence stream on the control page, `/evidence` table and record dialog | `demo: true` → badge                                                             |
| `GET /v1/controls/export`, `GET /v1/admin/evidence/export`                                                                  | new last column `demo` (`true`/`false`), so earlier columns keep their positions |
| OSCAL SSP                                                                                                                   | `demo-seed` prop (atlas ns) on the implemented requirement                       |
| OSCAL AR                                                                                                                    | `population-<id>-sampled-<n>-demo-seed` prop per sampled demo record             |

- The JSON fields are `omitempty`. Real rows serialize exactly as before, so
  the recorded contract goldens are unchanged.
- The catalog view of `/controls` (`/v1/anchors`) shows anchors, not
  controls, so it carries no mark.
- `GET /v1/controls` without `include=state` feeds the risk-form picker, not
  an auditor view, so it is unchanged.
- The audit-sample export (`internal/api/audit/export.go`) is not marked
  here: it lists sample populations, and the OSCAL AR already marks their
  demo members. Worth a follow-up if auditors use that CSV directly.

The OSCAL change adds two proto fields:

- `ControlImplementation.demo = 8`
- `SamplePopulation.demo_evidence_ids = 6`

Go bindings were regenerated with `buf generate` (remote plugins pinned in
`buf.gen.yaml`). The Python stubs were regenerated with the bridge's locked
`grpcio-tools`. Both diffs are additive; the Python protobuf version header
is unchanged.

## D6 — Test placement

The real-database checks for `demoprov` and for both exports live in
`internal/demoseed/provenance_integration_test.go`. That package already has
the in-place fixture and is enrolled in CI (leg B1). `demoprov` itself gets
pure-Go unit tests and a 95 floor (measured 100%), with no integration file.
That avoids enrolling a new package for a test that needs a seeded tenant.

There is no Playwright spec for the badge: the e2e bring-up has no in-place
demo tenant to point at. The Go integration tests prove the API sends
`demo: true` for exactly the seeded rows, and the vitest row test proves the
page maps it.
