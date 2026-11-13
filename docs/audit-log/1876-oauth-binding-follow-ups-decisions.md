# Decisions log — #1876: OAuth client binding follow-ups

- detection_tier_actual: manual_review
- detection_tier_target: integration

Ticket: security-atlas#1876, from the #1854 review (#1739). Three follow-ups:
rows already in the all-zeros tenant, public clients bound by the upgrade, and
read-only POST endpoints under the `tenancymw` write refusal. The gaps were
found in review of #1854, and the ledger deletion in the first cut of this
work was found in review of #1881 (D4), which is where the first tier above
comes from. An integration test that wrote through an unbound
client and then looked for the rows would have caught the leftover-rows gap.

## D1 — A CLI listing with a guarded purge, and no reassign

`atlas-cli oauth unbound-rows` lists, per tenant-scoped table, the rows whose
`tenant_id` is `uuid.Nil`. Listing is the default, and it only reads (a READ
ONLY, REPEATABLE READ transaction). `--purge` deletes the purgeable rows (D4),
and only with `--confirm-rows <n>`, where `n` must equal the purgeable total
that a listing in the same transaction counts. A mismatch deletes nothing. A
purge runs in one REPEATABLE READ transaction, prints each table it purged
and kept, and records itself in `tenant_data_purges` (D5). Any failure rolls
everything back.

The tables come from the catalog: every ordinary or partitioned table outside
the system schemas with a `uuid` `tenant_id` column. It isn't a hand-kept
list, so a table added later is covered without changing this code. (The
first cut read only `current_schemas(false)`, and review asked for every
non-system schema.)

Foreign keys between tenant-scoped tables are composite `(tenant_id, id)` and
mostly `RESTRICT`. The purge deletes in name order, each table under a
savepoint. A table whose delete trips a foreign key is retried on the next
pass, once its children are gone. A pass that deletes nothing fails the purge.
This needs no dependency graph. The integration test covers it with
`populations` (the parent, sorted first) and `samples` (the child). Both are
children of `controls`, so the test holds even when leftover all-zeros
evidence in a shared database retains `controls`.

Reassign is left out. Moving rows to a real tenant means rewriting `tenant_id`
on every row in a reference chain in one statement, because the composite keys
are not `ON UPDATE CASCADE`. A moved row can also collide with the target
tenant's own row on a `(tenant_id, ...)` unique key, such as `controls`' "one
active version per bundle". The data that lands here comes from re-runnable
machine writes (`controls upload`, evidence pushes), so the runbook re-creates
it with a bound client and then purges. A reassign command can follow if
someone needs one.

The code lives in `internal/auth/oauthclient` (`unboundrows.go`). The rows are
what unbound OAuth clients left behind, and that package's integration suite
is already enrolled (shard B1), so this needs no new package and no new floor.

## D2 — Reads under the all-zeros tenant stay allowed, for now

The ticket asks whether to refuse reads under the all-zeros tenant once every
install's clients are bound. Not in this change. With the purge available, an
install that has run it has nothing in the all-zeros tenant, so an unbound read
returns empty results and leaks nothing. Refusing reads would also turn an
unbound client's harmless GET into a 403 on installs that have not bound their
clients yet. Revisit when the upgrade path makes binding universal.

## D3 — Read-only POSTs keep the refusal

Of the 74 bearer-tier POST routes in `internal/api/openapi/routes.go`, three
write nothing (traced to the store):

| Route                                               | Handler                                                                              |
| --------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `POST /v1/audit-periods/{id}/oscal-export`          | `oscalexport.Export`: read-only aggregate, then serialize and sign                   |
| `POST /v1/audit-periods/{id}/oscal-export:download` | `oscalexport.Download`: the same export, as an attachment                            |
| `POST /v1/questionnaires/{id}/export-pdf`           | `questionnaires.ExportPDF`: two read transactions, then an in-memory chromedp render |

Others that sound like reads write: `/v1/risks/aggregate` and
`/v1/aggregation-rules/evaluate` (insert risks and evaluation rows),
`/v1/controls/checklist:generate` (persists sections), `/v1/populations`
(insert plus audit row), `/v1/calendar/subscription` (issues a credential).
There are no `:search` or `:query` POSTs.

Unbound clients do not keep the three. Under the all-zeros tenant each would
produce an export of an empty (or leftover) tenant: no client has a use for
that, and the OSCAL export signs it, which with the keyless signer means a
transparency-log entry. So `isSafeMethod` stays method-based, with the
inventory recorded at the function so a future read-only POST is weighed the
same way.

## D4 — Append-only ledgers, and the rows they reference, are never purged

Review of #1881 blocked the first cut. It hard-deleted all-zeros rows from
`evidence_records` and `evidence_audit_log` over BYPASSRLS.
ADR-0012 (property 1) allows disposal from the evidence ledger only by
tombstone, and ADR-0020 names privileged BYPASSRLS SQL on the migrate DSN,
with no audit trail, as "the worst of every available world". The purge now
keeps:

- **Ledger tables.** These are tables the application itself cannot delete
  from: no RLS policy with `FOR DELETE` or `FOR ALL`, or a user `BEFORE
DELETE` row trigger. The rule comes from the schema, so a new append-only
  table is covered by the same migration that makes it append-only. On the
  current schema it selects 42 tables. They include every `*_audit` and
  `*_audit_log` table, `evidence_records`, `control_evaluations`,
  `control_drift_snapshots`, and the two trigger-guarded logs
  (`action_plan_audit_log`, `change_audit_log`), whose triggers would
  otherwise abort the purge. It also selects tables such as `incidents` and
  `changes` that the application never deletes. Keeping those is the
  conservative error.
- **Tables a kept row references.** If any kept row references a table's
  all-zeros rows, through any foreign key, that table is kept too, and the
  rule is applied until nothing changes. Without this, a purge of `controls`
  either fails on `evidence_records`' `RESTRICT` key, or cascades into
  `control_evaluations` (`ON DELETE CASCADE`) and `evidence_records.scope_id`
  (`SET NULL` from `scopes`). Thirteen ledger foreign keys cascade or set
  null today. The check counts the referencing rows in any tenant, so a
  single-column key from another tenant's ledger row also keeps its parent.

Retained tables are listed with the reason and don't count toward
`--confirm-rows`. As a belt-and-braces guard, the purge also compares every
ledger table's total row count, across all tenants, before and after, and
rolls back if any changed.

`TestLedgerTables_CoverAppendOnlyTables` fails when a table named
`*_audit`, `*_audit_log` or `*_log`, or one of the ledgers ADR-0012 names,
isn't classified as a ledger. Three `*_log` tables carry a deliberate
`tenant_delete` policy (`channel_delivery_log`, `email_delivery_log`,
`staleness_rollup_log`: delivery and rollup bookkeeping) and are exempted by
name in the test. A new one must be added there on purpose. I watched the
probe fail on a scratch `probe_audit_log` with a `FOR ALL` policy, then pass
once it was dropped.

## D5 — A purge leaves a platform-level record

There is no platform-level audit table a CLI purge can write to. The unified
audit sink (`internal/audit/sink`) is an opt-in stream that the server
process configures, and `super_admin_audit_log` is shaped for role grants.
So migration `20260813000000_tenant_data_purges.sql` adds
`tenant_data_purges`, one row per committed purge, written in the purge's own
transaction. It holds the time, `session_user`, the OS user, the per-table
counts purged and kept (JSONB), and the total. It is platform-global (no
`tenant_id`, so `audit-rls` does not apply, and no grant to `atlas_app`), and
a `BEFORE UPDATE OR DELETE` trigger makes it append-only, as on
`change_audit_log`. A purge that rolls back leaves no row, because nothing
happened. Cascades into tables without a `tenant_id` are not counted, and the
runbook says so.
