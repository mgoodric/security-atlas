# 2171 — Every route belongs to a module: decisions log

Issue: mgoodric/security-atlas#2171 (spec #2166, after the #2170 tracer).
Every optional module can be switched off, and every server route belongs to
a module, core or optional.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced. Removing Risk's
  `/v1/controls/*/action-plans` pattern makes
  `TestModules_EachOptionalModuleSwitchesOff` fail (the nested route answers
  200 with Risk off); renaming `/v1/incidents` in the registry makes
  `TestEveryRouteBelongsToAModule` and `isa/probes/modules-route-coverage.sh`
  fail (exit 1), and both pass again once restored.
- **detection_tier_target:** unit (the route guard) and integration (the
  gate through the real router).

## Decisions

### D1 — route patterns, most specific wins

A route pattern owns itself and every path below it, segment by segment; `*`
matches one segment, and the last segment also owns its `{id}:verb` form.
When several patterns match, the one with the most segments wins, then the
most literal segments. So `/v1/controls/*/risks` (Risk) beats `/v1/controls`
(Controls), which is how a nested route belongs to the module whose data it
returns. Two different owners tied for most specific is an error
(`ErrAmbiguousModule`) that the guard test fails on, as is a path no pattern
owns (`ErrNoModule`).

### D2 — classification of the core routes

The spec names the core modules; the routes it does not place are put here:

| Routes                                                                                                                                                           | Module                         | Why                                                                |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------ | ------------------------------------------------------------------ |
| `/v1/aggregation-rules`, `/v1/org_units`, `/v1/themes`, `/v1/decisions`                                                                                          | Risk                           | risk hierarchy, risk themes and the decision log are Risk features |
| `/v1/me/acknowledgments`, `/v1/admin/policies/*`                                                                                                                 | Policies                       | returns policy acknowledgments / policy data                       |
| `/v1/admin/vendors/*`, `/v1/admin/exceptions/*`                                                                                                                  | Third-party risk, Exceptions   | exports of that module's data                                      |
| `/v1/artifacts`, `/v1/schemas`, `/v1/admin/evidence/*`                                                                                                           | Evidence                       | evidence artifacts and the evidence-kind registry                  |
| `/v1/anchors`, `/v1/requirements`, `/v1/csf`, `/v1/mapping-overlays`, `/v1/oscal/imported-profiles`, `/v1/admin/crosswalk-edges`, `/v1/admin/framework-versions` | Frameworks and the SCF catalog | catalog and crosswalk data                                         |
| `/v1/controls/*/effective-scope`, `/v1/controls/*/applicability`                                                                                                 | Scopes                         | scope data under a control path                                    |
| `/v1/audit-notes`, `/v1/populations`, `/v1/samples`, `/v1/walkthroughs`, `/v1/me/audit-period(s)`, `/v1/admin/audit-periods/*`, `/v1/admin/samples/*`            | Audit periods                  | audit-period workspace data                                        |
| `/v1/activity`, `/v1/calendar`, `/v1/calendar.ics`, `/v1/search`, `/v1/metrics`, `/v1/saved-views`                                                               | Dashboard                      | combined views (filtered per module in #2173)                      |
| `/v1/admin`, `/v1/me`, `/v1/tenants`, `/v1/mcp`, install, version, test, `/auth`, `/oauth`, `/scim`, `/.well-known`, `/health`, `/ready`, `/metrics`             | Settings & admin               | platform, identity and administration                              |

MCP write proposals (`/v1/mcp/*`) stay core here; applying a proposal aimed
at a switched-off module is #2175's job. MCP read tools call the REST API,
so the gate covers them (risks, policies and exceptions included).

### D3 — features that already gate a route; the rest gate through their module

A feature is on only when its module is on and its own switch is on, and
its switch keeps its stored value while the module is off. The registry
gives a feature its own routes only where its switch gated something
before or is new: Vendor claims (`vendor.claims`, formerly `oscal.export`)
and the OSCAL SSP and POA&M export (`audit.oscal_export`, new, default on).

Risk themes, risk hierarchy, the decision log, policy acknowledgments,
control bundles and audit sampling are listed as features, so Admin ›
Features groups them, but their switches gate no route yet: their routes
are gated by their module. Making those switches bite now would hide routes
from any tenant that had stored them off while they did nothing. The
acceptance test for hierarchy ("Risk off hides hierarchy; Risk back on
restores it without changing its switch") holds through the module.

### D4 — `oscal.export` becomes `vendor.claims`; the real export gets its own key

Reusing `oscal.export` for the SSP/POA&M export would have turned the
vendor-claims value (default off) into the export's value for every tenant
that stored it. Migration `20261128000000` renames each stored row to
`vendor.claims` (category `vendor`), value unchanged; the append-only audit
log keeps the old key on history rows. The down migration renames back and
refuses, rather than deletes, if a `personnel.enabled` or
`audit.oscal_export` row exists.

### D5 — module switches that did nothing are reset to on

`risk.enabled`, `vendor.enabled`, `policy.enabled` and `exceptions.enabled`
were seeded and stored but read by nothing. Now they hide their module. The
ticket requires existing tenants to keep today's behaviour, so the migration
sets any stored `false` on those four keys to `true`, writing a
`feature_flag_audit_log` row (actor `migration:20261128000000`) for each. The
down migration does not undo this: the switches hid nothing before.

### D6 — fail-open on a flag-store error, for every module

`Enabled` falls back to the Seed default when the flag store errors, and
the gate inherits that per switch: the data-bearing modules that default on
(Risk, Third-party risk, Policies, Questionnaires, Personnel security,
Exceptions) fail open, which keeps the existing anti-criterion (RLS is the
security boundary, not the switch; a DB blip must not hide a module).
Board reporting and Vendor claims default off, so they fail closed. #2166
asks for nothing stricter.

### D7 — the gate checks the raw path too

chi routes on `URL.RawPath` when it is set, so an id containing `%2F`
(`/v1/controls/a%2Fb/risks`) routes to the nested Risk handler while the
decoded path has an extra segment and resolves to Controls. The gate
resolves both the decoded and the raw path and refuses if either is off.

### D8 — `GET /v1/me/modules` lists features for every caller

Each optional module now carries `features`, each with its effective state
(module AND feature) and pages. The spec says admins get the features; the
web app's page gate needs the Vendor claims page's state for every member,
so the states and pages are sent to everyone. Switch metadata (who changed
it, when) stays on the admin-only `GET /v1/admin/features`. Narrowing the
list per caller is #2172 (module access).

### D9 — Admin › Features groups by module from the server

`GET /v1/admin/features` items carry `module`, `module_name`,
`module_switch`, `module_core` and `feature_name` from the registry. The page
renders one card per module: the module switch heads it, features are nested
under it, and a core module's card says it is always on. The page keeps no
module map of its own.

### D10 — the `Gate` primitive is removed

Nothing mounted `featureflag.Gate` after #2170. It is deleted with its unit
tests; `features/gate_integration_test.go` now drives `ModuleGate`, keeping
its RLS-isolation case. The empty `root.Group` wrappers in
`register_board.go` and `register_oscal.go` are removed (the route goldens
are unchanged, since a group without `Use` adds no middleware).

### D11 — the web pages of every optional module are gated

The Risk, Action plans, Vendors, Policies, Personnel security and Exceptions
layouts are wrapped in `FeatureGate`, as Questionnaires was in #2170, so a
direct link to a switched-off module shows the calm off state.
