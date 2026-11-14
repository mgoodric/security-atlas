# Decisions log — #2153: demo provenance follow-ups

- detection_tier_actual: integration
- detection_tier_target: integration

Ticket: security-atlas#2153, follow-ups from the review of #2124 (#2043).

The integration bug below (D4) was found by the new legacy-teardown test,
not in production.

## D1 — The audit-sample export counts demo members

`GET /v1/admin/samples/export` gains a last column, `demo_evidence_count`:
how many of the sample's drawn records (`sample_evidence`) the in-place demo
seed added. Earlier columns keep their positions, as with the `demo` column
#2043 D5 added to the controls and evidence exports.

A count, not `true`/`false`: a sample row is a draw of N records, and a
partly-demo sample is the case an auditor most needs to see. The count reads
`demoprov.Lookup` in the export's own tenant transaction, the same lookup
the OSCAL AR uses, so the CSV and the AR cannot disagree. Formula-injection
neutralisation is unchanged: the column passes through the shared
`internal/export` CSV sanitizer like every other cell, and it only ever
holds digits.

The demo counts are read only for an export under the row cap. An over-cap
export is refused with 413 before any row is written.

## D2 — `DemoSeededIDs` names the tenant

The query now carries `tenant_id = $tenant_id` beside RLS. `demoprov.Lookup`
reads the tenant from the context (`tenancy.TenantFromContext`), so no call
site changes, and a context with no tenant is an error rather than an
unscoped read. Every caller already runs inside a tenant transaction built
from that same context.

## D3 — A CLI path for pre-#2043 button tenants, not a docs-only procedure

The ticket allowed documenting a manual removal or adding a super-admin-only
CLI path. A documented procedure would be raw SQL against a BYPASSRLS DSN
with every safety check left to the reader, so the CLI path was chosen:
`atlas-cli demo teardown-legacy`.

- **Super admin only.** The CLI has no session, so the gate is
  `--super-admin-email`, which must resolve to exactly one active user with
  a `super_admins` grant. That user is the actor on the teardown audit rows.
  The DSN is still the real credential, as for every `demo` verb; the email
  makes the deletion attributable and keeps a non-super-admin from being
  named as its author.
- **Only a legacy button tenant.** Another tenant must hold a
  `demo_seed_apply` mark naming the slug (where the pre-#2043 button wrote
  it), and the tenant must have no mark of its own and no in-place rows.
  Those last two belong to `demo teardown` and are refused with a pointer to
  it.
- **The mark belongs to this tenant (review B1).** A slug match alone would
  accept a later tenant that took the slug after the legacy tenant was
  gone. The button created the tenant and wrote the mark in one
  transaction, so the mark's `occurred_at` is never earlier than
  `tenants.created_at`; a mark that is earlier refuses. A
  `demo_seed_teardown` naming the slug (in `me_audit_log` or
  `super_admin_audit_log`) since the tenant's creation also refuses: the
  slug has changed hands since.
- **No non-demo data.** There is no per-row record of what the button
  wrote, so "non-demo" is defined by what a real organisation would hold:
  - any user without `demo_only` refuses, and so does a tenant with no
    users at all (the seed always writes demo users; a machine-fed tenant
    needs none, review B1);
  - an `oauth_clients` row bound to the tenant refuses. That table carries
    `bound_tenant_id`, not `tenant_id`, so the table scan below cannot see
    it;
  - any row in a tenant-scoped table outside `teardownTables` refuses,
    naming the tables. Audit trails are excepted, as everywhere in
    `demoseed`.
  - The evaluator's read models (`control_evaluations`,
    `evidence_freshness`) count as demo: the button ran
    `EvaluateSeededTenant` after seeding, and they are derived from demo
    controls and evidence.
  - Rows a visitor added while signed in as a demo user count as demo. No
    real account exists in the tenant to have written anything else.
- **One SERIALIZABLE transaction.** The checks and the delete share one
  transaction at `SERIALIZABLE`. The app writes at `READ COMMITTED`, so
  the guarantee is snapshot isolation, not serializability against those
  writers:
  - the delete removes only rows visible to the checks' snapshot, so a row
    committed after the checks is never deleted;
  - a concurrent update or delete of a row the teardown deletes fails it
    with 40001. The command reports "concurrent write detected; nothing was
    deleted; rerun" (`ErrConcurrentWrite`);
  - a concurrent insert into a table with an FK to `tenants` blocks on the
    tenant row's `FOR UPDATE` lock. One into a table with no FK to
    `tenants` is left behind as an orphan, not deleted.
- **Undefined tables fail safe.** `deleteDemoTenant` skips a 42P01, but
  Postgres has already aborted the transaction, so the next statement fails
  with 25P02 and nothing is deleted. The old comment called this
  "best-effort"; it is not, and the comment now says so.
- **`--dry-run`.** Runs every check and the delete, prints the rows each
  DELETE removed per table (FK cascades are not counted), and rolls back.
  The teardown audit rows roll back with it.

The delete reuses the whole-tenant path (`deleteDemoTenant`, extracted from
`teardownTenant`), so both deletions sweep the same tables.

## D4 — `org_risk_categories` joins `teardownTables`

`risks_category_sync_trg` seeds a tenant's risk categories when its first
risk lands, so the demo seed writes them indirectly. They were not in
`teardownTables`, and the table has no FK to `tenants`, so a whole-tenant
teardown left them orphaned. The legacy check found them as "rows the seed
does not write". They now sit after `risks`, whose `(tenant_id,
category_id)` references them. For an in-place seed this changes nothing:
the categories are recorded in `demo_seed_rows` only when the seed created
them.
