# 2173 — Combined views show only modules you can see: decisions log

Issue: mgoodric/security-atlas#2173 (spec #2166, ADR 0025). After #2171
(every route belongs to a module) and #2172 (module access per member).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced. With the views
  unwired (`s.moduleViews = nil`) the four `TestCombinedViews_*`
  integration tests fail (vendor reviews on a switched-off calendar, policy
  items in a narrowed member's calendar, ICS, search, Activity and export,
  risks in the export); wired, all four pass.
- **detection_tier_target:** integration (the production router, real
  Postgres under RLS, the live user_roles resolver).

## Decisions

### D1 — one helper, keyed by route

`moduleaccess.Views` answers "may this viewer see an item from the module
that owns route X": the module and feature are switched on
(`featureflag.RouteOn`, what `ModuleGate` checks) and an optional module is
in the viewer's access (`Access.Has`, what `Gate` checks). Each view names
its item kinds by a route the registry places (`/v1/policies`,
`/v1/vendors`, ...), as `GET /v1/approvals` already did, so no view keeps a
second map of which module owns what.

`Viewer.Modules()` resolves every optional module once, for views that
filter rows inside a transaction (Activity, Metrics): the per-row check
then reads nothing from the pool. It answers per module and ignores
feature switches; no Activity kind or metric belongs to a feature today.

### D2 — the ICS feed reads its subscriber at fetch time

The ICS token is a calendar-only credential with no roles. Subscribing now
mints it as the subscriber's own key (`credstore.IssueOwned`: its UserID is
the person, persisted as `api_keys.issued_by`; no migration). The
subscribe handler's comment already said the UserID was meant to carry the
subscriber; the old call passed the empty string as the scope argument
instead. `api_keys.minted_by` was rejected: its column comment says it is
attribution only and never read for authorization.

Owning the key has three effects, checked:

- the user gate refuses the feed once the person is deactivated, and
  deactivation's `RevokeUserAPIKeys` revokes it;
- the policy-acknowledgement denominators count only admin keys or keys
  with owner roles, and a calendar key has neither;
- the key is still no general bearer: since slice 326 the HTTP API
  authenticates JWTs only, and the integration test pins a 401 for the ICS
  token on `GET /v1/policies`.

At every fetch the feed reads the owner's live `user_roles` and
`member_module_access`, so narrowing access takes effect without
re-subscribing. It fails closed: a token with no owner (minted before this
change, or by a credential that names no user) and an owner with no live
role see core modules only. The changelog fragment carries the
re-subscribe upgrade note.

The feed still drops the OPA-gated types (risk, action plan, vendor,
exception, offboarding) for everyone, as since #2130/#2232. Asking OPA on
the subscriber's behalf is a separate change; this ticket only narrows.

### D3 — Activity filters after the page is cut

`GET /v1/activity/unified` drops rows of a hidden module's kind (exception,
aggregation rule, taxonomy, risk) and authorization decisions whose
`target_type` (the OPA resource type) names a hidden module's route. It
filters after the cursor is derived, so paging is unchanged and a page can
come back short. Pushing the filter into the UNION query would need a
kind-to-module map in SQL, a second copy of the registry. The #2375 payload
gate is unchanged and runs first. The admin forensic surfaces
(`/v1/admin/audit-log/...`) are not filtered.

### D4 — search drops hidden types silently

A type whose module is hidden is removed from the request before the OPA
partition, so it appears neither in `hits`, `groups` nor `partial_types`.
`partial_types` means "results hidden, request access", which is wrong for
a switched-off module and leaks that the module has matches.

### D5 — the export hides the risks panel with Risk

The dashboard export's upcoming panel calls `calendar.VisibleTypes`, so it
matches the calendar. Its risks panel is empty when Risk is hidden. The
field rides on the existing `UpcomingTypes` value (`HideRisks`) to keep the
`PanelSource` seam to one argument.

### D6 — metrics by category, with per-metric overrides

The catalog has no module field. `risk` and `vendor` categories map to Risk
and Third-party risk; four metrics are overridden
(`exception_expiration_runway` to Exceptions, `policy_attestation_rate` to
Policies, `hire_onboarding_completion` and `offboarding_completion` to
Personnel security). Board attendance metrics stay core: they measure the
board, not the Board reporting module, which defaults off and would
otherwise hide them for every new tenant. List, detail and cascade are
filtered (the cascade prunes a hidden metric's subtree); the
observations, inputs and target routes under `/v1/metrics/{id}` are not.

### D7 — Approvals counts follow access

`GET /v1/approvals` already skipped switched-off types. It now also skips
types outside the caller's access, so the badge never counts what the
approve route would answer 403 to.
