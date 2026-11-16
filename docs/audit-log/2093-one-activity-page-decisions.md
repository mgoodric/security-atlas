# #2093: one Activity page, events as sentences, with a raw ledger toggle

detection_tier_actual: unit
detection_tier_target: unit

The one bug found during the work was `lib/no-origin-self-fetch.test.ts`
naming the deleted `app/audit-log/layout.tsx` as a file its scan must
reach. The unit tier caught it, which is where it belonged.

## D1. One endpoint: `/v1/activity/unified`

The page reads through `/api/activity` for every caller. For admin,
auditor and grc_engineer the activity handler's row-visibility predicate
short-circuits, and the result set is the one `/v1/admin/audit-log/unified`
returns (`internal/api/adminauditlog/activity.go` header). For viewer and
control_owner it is the narrower set they already saw on `/activity`. So
redirecting `/audit-log` and `/admin/audit` here widens nobody's access:
each caller hits an endpoint they could already reach, and gets the rows
they already got.

The admin BFF `/api/audit-log/unified` has no UI caller now. It stays
in place, because removing an API route is a separate decision and the
export BFF beside it is still used.

## D2. Export: shown by the old guard's predicate, gated by the backend

`/v1/admin/audit-log/export` admits fewer callers than the activity
endpoint, and admits them with a different check. The server shell runs
`canReachAuditLog` (the predicate the retired `/audit-log` layout used,
moved verbatim to `web/lib/api/audit-log-access.ts`, with its 14-case
test) and passes `canExport` to the client. The export bar renders only
when it is true. Exactly the callers who could open the old page see the
bar. The backend is still the authority, so a viewer who hand-builds an
export URL gets the same 403 as before.

The export takes no `include_reads` filter and returns every row in the
window, as it did on `/audit-log`.

Known gap, not fixed here: on Activity the actor filter accepts the `me`
sentinel, and the export BFF does not resolve it, so an export with
`actor=me` matches nothing. The old page never produced `me`. This is a
follow-up for the export BFF.

## D3. What a "saved filter" is

`/audit-log` and `/activity` keep their filters in the URL; there is no
server-side saved view for either (`/v1/saved-views` covers `/controls`
only). A saved filter is a bookmarked or shared link. The `/audit-log`
redirect copies the whole query string onto `/activity`, and both pages
used the same keys (`from`, `to`, `actor`, `kind`), so an old link opens
the same filter. The Playwright spec loads one and checks the chips, the
date input and the query the page sends.

## D4. Sentence templates read only the envelope

`web/lib/activity-sentences.ts` maps each kind to a template of
`(kind, action, target_type, target_id)` and never reads `payload_json`.
Payloads are null for rows whose source the caller cannot read (#2379),
and risk rows carry only changed-field names (#2367), so a sentence built
from the payload would either break or show something the envelope does
not justify. The actor is rendered by `<Person>` (#2383).

`decision` is mapped explicitly to the generic sentence
(`recorded "read" on controls …`): it is read telemetry, and its allow /
deny outcome lives in the payload. A kind the backend adds later falls
back to the generic sentence at runtime; the unit test fails until it is
mapped to a template or to `GENERIC`.

Past tenses: a small table covers the actions a suffix rule gets wrong
(`submit`, `carry_over`, `set_default`, `duplicate`). A dotted action keeps
its prefix as a qualifier: `predicate.update` reads "updated framework
scope … (predicate)".

## D5. Record links: controls only

A link must point at a route the reader can open. Controls are the only
record type every role is admitted to read (`policies/authz/*.rego`:
viewer reads controls, policies, risks, framework-scopes and more but not
exceptions; control_owner reads controls, evidence, policies and
walkthroughs but not risks; auditors are denied risks). Every other target
renders as plain text, with the full id in the tooltip and in the raw
ledger. A role-aware link map is possible later, but it would copy the
authz policy into the frontend.

## D6. Grouping and time

Rows are grouped by the viewer's local calendar day, labelled Today,
Yesterday, then a long date. Grouping is done in the browser, where the
viewer's timezone is known. Each row's time is a `<Timestamp>` (#2369):
relative within 7 days, absolute after, and the exact UTC instant in the
tooltip.

## D7. Accessibility

Each day is a `<section>` labelled by its heading. The expander is a real
button carrying `aria-expanded` and `aria-controls` (it stops propagation
in the ledger, where a row click also toggles). The ledger's first column
header gets screen-reader text. The Playwright spec runs the axe gate with
rows expanded in both views, which is the interactive-state sweep #2397
asks for on this page.
