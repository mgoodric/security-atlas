# 2414 — EntityPicker follow-ups: decisions log

Issue: mgoodric/security-atlas#2414, from the review of #2409 (#2086).
Rules from the dispatcher on the ticket: list machine and system actors
from the tenant's own activity, server-side, labelled via `<Person>`, with
no free text; return the caller's own private framework versions under RLS
with an explicit tenant predicate as a second leg (ADR 0026); an isolation
test across two tenants; users without a display name get the local-part.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — private frameworks come with their versions

A tenant-private version (`framework_versions.tenant_id = <tenant>`)
can hang off a global framework or off a tenant-private framework (the
demo seed creates both rows private). Listing only the versions would
drop the second kind. So `?include=versions` lists the catalog plus the
caller's own frameworks (`ListFrameworksForTenant`, already used by the
board brief) and the catalog plus the caller's own versions
(`ListFrameworkVersionsForTenant`, new), grouped in Go. Both run in one
tenant transaction, so RLS (`tenant_or_catalog`) binds. The plain
`GET /v1/frameworks` stays catalog-only: the SoA tab and other readers
of that list expect the catalog, and widening it is not in this ticket.

The old path ran one versions query per framework. The new one runs two
queries in total.

### D2 — `GET /v1/activity/actors`, its own query, not a paged scan

The actor list has to come from the tenant's activity under the feed's own
visibility rules, or a viewer would be offered actors whose rows they
cannot see. Paging the feed to collect actors would read every row in the
window. Instead `ListUnifiedAuditMachineActors` mirrors the feed's sixteen
sources, projecting only what the rules read (actor, kind, action,
target_type, request_path), and returns the distinct tuples for machine
actors. The handler applies the same #2173 module filter
(`visibleEntries`) to those tuples, so an actor seen only in a hidden
module's rows is not listed.

Duplicating the source list is a drift risk: a new audit-log table added
to the feed and not here would leave its actors out of the picker.
`TestMachineActorsQueryMirrorsUnifiedLog` (unit, no database) fails when
the two queries' `FROM *_log` sets differ.

The endpoint writes no `me_audit_log` meta-audit row. It returns actor
identifiers, not rows, and the feed query the page makes alongside it is
already audited.

### D3 — which actors count as machine actors

`system`, `system:<process>` and `oauth_client:<id>`, the shapes named in
the ticket and the ones `<Person>` can label. People and API keys come from
`/v1/people`. Other non-person strings in the ledger (`seeder`,
`bootstrap-admin`) are not listed; a deep link to one still works, shown
as the raw value, as before.

### D4 — `<Person>` names an OAuth client

`resolvePerson` read `oauth_client:<id>` as "Unknown". It now reads
"OAuth client", the client ID in the tooltip (`kind: "oauth_client"`). The
picker labels each machine actor with that text and shows the process or
client ID as the hint, so two "System" actors stay apart and the hint is
searchable.

### D5 — a deep-linked machine actor is always an option

`?actor=system` or `?actor=oauth_client:<id>` for a window that holds none
of its rows is added to the options anyway, so it shows its label and a
blur keeps it. Typing an arbitrary value still fails the paste check: no
free text.

### D6 — the name fallback is already on the server

`GET /v1/people` has returned the email's local-part for a user with no
display name since #2383 (`people.DisplayName`), so the picker already
lists such a user by it. A user is dropped only when the email is blank
too, which `users.email NOT NULL` makes an empty string at worst. A
vitest case now pins that a local-part name is listed.
