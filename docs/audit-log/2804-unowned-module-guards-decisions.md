# 2804 — Unowned routes and kinds stay visible; build-time guards: decisions log

Issue: mgoodric/security-atlas#2804, from the #2718 (PR #2788) review.
After #2171 (every route belongs to a module), #2173 (combined views
filter by module) and #2477 (decisions placed by request path).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced; one accepted
  gap is recorded under D3.
- **detection_tier_target:** unit (registry and map shape, checked without
  services).

## Decisions

### D1 — an unowned row is shown, by design

`moduleaccess.ModuleSet.Sees` shows a path the registry places under no
module (`ErrNoModule`), and `entrySeen` shows a kind absent from
`kindModuleRoute`. Matt kept that on 2026-11-20 (the dispatcher's
recommendation on #2718): hiding a row because the registry has a hole
hides data for a build mistake, and the #2788 SQL filter matches the Go
behaviour. The gap is closed at build time instead: a new route or kind
left unregistered fails a unit test.

### D2 — the route guard already exists; no second route walk

The ticket's first criterion (walk every registered route; fail when one
has no `featureflag.Modules` owner; allowlist module-free routes) is met by
`internal/api` `TestEveryRouteBelongsToAModule` (#2171), which walks the
live chi router and fails on `ErrNoModule` or `ErrAmbiguousModule`, and
`TestRouteModules_MatchGolden` (#2352), which pins each route's owner.
There are no module-free routes: auth, health, me, admin, scim, oauth and
`.well-known` belong to the core `settings_admin` module, whose `Routes`
list is the commented allowlist. Adding a second walk with its own
allowlist would be a second mechanism for the same fact. Red-checked by
deleting the Exceptions module from `featureflag.Modules`: the test fails
naming `GET /v1/exceptions`, `POST /v1/exceptions` and the other seven
exceptions routes.

### D3 — two new guards, in `adminauditlog/modules_owner_2804_test.go`

- `TestEveryKindHasAModuleOrIsCore`: every `unifiedlog.AllKinds` entry is
  in `kindModuleRoute` (an optional module's kind) or in `coreKinds` (a
  commented list of core kinds and their module), and in only one. Before
  this, a new optional-module kind left out of `kindModuleRoute` silently
  became core.
- `TestEveryDecisionTargetTypeHasAModule`: for every route in
  `internal/api/testdata/routes_modules.golden` (kept equal to the router
  by `TestRouteModules_MatchGolden`), the target type
  `authz.BuildInput` derives falls back through `decisionRoute` to a route
  some module owns, or is listed in `unplacedTargetTypes`. A stale
  allowlist entry fails too.

The decision test reads the golden from the sibling package rather than
walking the router, because `decisionRoute` is unexported in
`adminauditlog` and the router is built in `internal/api`; the golden is
the router's table under an existing guard.

`unplacedTargetTypes` holds `""`, `auth`, `oauth`, `scim` and
`.well-known`, all from `settings_admin` (core) routes, so showing their
rows is what the registry would answer anyway, and `oscal`. `/v1/oscal/*`
is split between frameworks (core, imported profiles) and
third_party_risk's vendor_claims feature (component definitions and
claims). A legacy decision row (before #2477 recorded `request_path`) with
target type `oscal` cannot say which, so it is shown while vendor claims
is off. This is accepted under D1: only pre-#2477 rows take the fallback.

Red-checked by deleting `KindVendor` from `kindModuleRoute` (fails:
`kind "vendor" has no module`) and by deleting the Exceptions module
(`TestEveryDecisionTargetTypeHasAModule` fails for each exceptions route
with target type `exceptions`).
