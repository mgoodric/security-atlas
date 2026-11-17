# 2477 — Module views follow-ups: decisions log

Issue: mgoodric/security-atlas#2477, from the review of #2476 (#2173).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The #2476 review found all four
  gaps. With each fix disabled, the new tests fail. Activity lists a
  `/v1/me/acknowledgments` decision and a `/v1/controls/{id}/policies`
  decision while Policies is hidden. An ICS token can call
  ConnectorRegistry Register and List and evidence Push.
- **detection_tier_target:** integration. Activity and the metric
  sub-routes run against real Postgres under RLS. The gRPC check runs
  in-process over bufconn.

## Decisions

### D1: a decision row is placed by its recorded request path

`decision_audit_log.request_path` (`r.URL.Path`, recorded by `authzmw`)
reaches Activity in the row's payload. `decisionRoute` reads it, and the
module registry places it, so `/v1/me/acknowledgments` and
`/v1/controls/{id}/policies` resolve to Policies. Only a row with no
recorded path (an empty or absent `request_path`) falls back to
`"/v1/"+target_type`. A row that has a path never uses its type. A
decision with `target_type=policies` on `/v1/controls/{id}` stays shown
while Policies is hidden. Both the unit test and the integration test pin
that case.

### D2: filter before the payload gate

The #2375 payload gate can clear `payload_json`, which carries the path.
Activity therefore filters by module first and then applies the gate to
the rows it keeps. The gate decides each row on its own, so applying it
to the filtered page gives the same result for every row shown.

### D3: metric sub-routes answer 404 after input validation

`ListObservations`, `GetTarget` and `UpsertTarget` call `hideMetric` just
before their own query, after request validation. A malformed request is
still a 400 and needs no database. `CreateInput` reuses the catalog row it
already reads. A metric with no catalog row is placed by its id alone
(`metricModuleRoute`). The sub-route's own lookup then answers whether the
metric exists. A views or catalog error answers 500 and never serves the
values.

### D4: Modules() reads feature switches

`ModuleSet` now records switched-off features as well as hidden modules.
That covers features of core modules too (`audit.oscal_export`,
`controls.bundles`), so `ModuleSet.Sees` gives the same answer as
`Viewer.Sees`. Only features that own routes are read, because a feature
with no routes owns nothing `Resolve` can place. A flag lookup that
returns an error fails the whole answer (500), as before.

### D5: gRPC refuses exactly-calendar credentials

`credstore.Credential.CalendarOnly()` is true when a credential's kinds
are exactly `{calendar.read.v1}`. `IssueOwned` sets no other marker on an
ICS token. `authInterceptor` refuses such a credential as Unauthenticated
on every unary RPC. A key whose kinds include `calendar.read.v1` among
others is a general key and still authenticates. A test pins this. The
constant moved to `credstore.KindCalendarRead`, and the calendar package
reads it from there, so both checks share one definition.
