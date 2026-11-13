# security-atlas#1897 — unbound-rows ledger classification hardening

Follow-up to #1876 / #1881 (second review). Hardens the classifier in
`internal/auth/oauthclient/unboundrows.go` (`tenantTablesSQL`) that decides
which tenant-scoped tables `atlas-cli oauth unbound-rows --purge` never
deletes from. None of these was a live gap on the current schema: on a
fully migrated database the two new signals reclassify no table, and every
tenant-scoped table is in `public`.

detection_tier_actual: manual_review
detection_tier_target: integration

## D1 — Only permissive DELETE policies count

A `RESTRICTIVE` policy narrows what permissive policies grant and grants
nothing itself, so a table whose only DELETE policy is restrictive is still
undeletable for the application. The policy check now requires
`p.polpermissive`.

## D2 — A missing DELETE grant marks a ledger, and so does a missing role

A table can be append-only by grant alone (a DELETE policy, but no
`GRANT DELETE` to `atlas_app`). The classifier now also asks
`has_table_privilege` for `atlas_app`. It looks the role up in `pg_roles`
rather than passing the name, so a database without an `atlas_app` role
classifies every table as a ledger and purges nothing, instead of the query
failing. When unsure, retain.

## D3 — The coverage guard reads every non-system schema

`TestLedgerTables_CoverAppendOnlyTables` listed only `public`. It now uses
the same schema filter as the classifier and compares by display name
(`schema.table` outside `public`), so a future `privacy.erasure_audit_log`
is held to the `*_audit_log` rule.

`TestLedgerTables_ClassifiesBySignal` builds scratch tables in the rolled-back
test transaction, one per signal, plus one in a scratch schema. Each case was
watched failing with its change reverted.
