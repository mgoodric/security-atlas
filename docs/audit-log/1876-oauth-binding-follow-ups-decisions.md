# Decisions log — #1876: OAuth client binding follow-ups

- detection_tier_actual: manual_review
- detection_tier_target: integration

Ticket: security-atlas#1876, from the #1854 review (#1739). Three follow-ups:
rows already in the all-zeros tenant, public clients bound by the upgrade, and
read-only POST endpoints under the `tenancymw` write refusal. No bug surfaced
during the work; the gaps were found in review of #1854, which is where the
first tier above comes from. An integration test that wrote through an unbound
client and then looked for the rows would have caught the leftover-rows gap.

## D1 — A CLI listing with a guarded purge, and no reassign

`atlas-cli oauth unbound-rows` lists, per tenant-scoped table, the rows whose
`tenant_id` is `uuid.Nil`. That is the default and only reads (a READ ONLY
transaction). `--purge` deletes them, and only with `--confirm-rows <n>`, where
`n` must equal the total a listing in the same transaction counts. A mismatch
deletes nothing. A purge runs in one transaction and prints each table it
purged with its row count; any failure rolls back everything.

The tables come from the catalog (every ordinary or partitioned table in the
search path with a `uuid` `tenant_id` column), not from a hand-kept list, so a
table added later is covered without touching this code.

Foreign keys between tenant-scoped tables are composite `(tenant_id, id)` and
mostly `RESTRICT`. The purge deletes in name order, each table under a
savepoint. A table whose delete trips a foreign key is retried on the next
pass, once its children are gone. A pass that deletes nothing fails the purge.
This needs no dependency graph, and the integration test covers it with
`controls` (parent, sorted first) and `evidence_records` (child).

Reassign is left out. Moving rows to a real tenant means rewriting `tenant_id`
on every row in a reference chain in one statement (the composite keys are not
`ON UPDATE CASCADE`), and a moved row can collide with the target tenant's own
row on a `(tenant_id, ...)` unique key, such as `controls`' one active version
per bundle. The data that lands here comes from re-runnable machine writes
(`controls upload`, evidence pushes), so the runbook re-creates it with a bound
client and then purges. A reassign command can follow if someone needs one.

The code lives in `internal/auth/oauthclient` (`unboundrows.go`). The rows are
the trail of unbound OAuth clients, and that package's integration suite is
already enrolled (shard B1), so no new package and no new floor.

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
