# 2174 — Links, ownership and pickers across modules: decisions log

Issue: mgoodric/security-atlas#2174 (spec #2166, ADR 0025). After #2171
(every route belongs to a module), #2172 (module access per member) and
#2173 (combined views).

## Detection-tier classification

- **detection_tier_actual:** playwright. The first cut treated a user
  with no `user_roles` row as having no optional module, which emptied
  the risk and action-plan owner pickers in the e2e tenant (D2). The
  same run showed four specs mocking `/api/people` by exact path, which
  the new `?module=` query fell past; their route patterns now accept a
  query string, as `activity.spec.ts`'s already did. With the
  server wiring removed (`WithModules` dropped from the people, vendors
  and incidents registrars), three of the four `TestModuleLinks_*`
  integration tests fail: the vendor flag stays false, the assignee
  options list the narrowed member, and the incident names the vendor.
  Wired, all four pass.
- **detection_tier_target:** integration (the production router, real
  Postgres under RLS, the live user_roles resolver).

## Decisions

### D1 — every cross-module link in an API response

Found by reading every wire type for ids or link arrays that name
another module's record. Links into a core module (controls, evidence,
scopes) are never hidden: core is always on and has no access to lack.

| Record (module)    | Link to (module)            | Where                                 | Handling                                                     |
| ------------------ | --------------------------- | ------------------------------------- | ------------------------------------------------------------ |
| Control (core)     | Policies (Policies)         | `GET /v1/controls/{id}/policies`      | Already refused by the module gates (404 off, 403 no access) |
| Control (core)     | Risks (Risk)                | `GET /v1/controls/{id}/risks`         | Already refused by the module gates                          |
| Control (core)     | Action plans (Risk)         | `GET /v1/controls/{id}/action-plans`  | Already refused by the module gates                          |
| Incident (core)    | Risks (Risk)                | `GET/POST /v1/incidents...` `links`   | Stripped: `RiskIDs` is `[]` when Risk is hidden (new)        |
| Incident (core)    | Vendors (Third-party risk)  | `GET/POST /v1/incidents...` `links`   | Stripped: `VendorIDs` is `[]` when TPR is hidden (new)       |
| Decision (Risk)    | Exceptions (Exceptions)     | `GET /v1/decisions/{id}` `linkage`    | Stripped: `exceptions` is `[]` when hidden (new)             |
| Decision (Risk)    | Risks, controls, predicates | same                                  | Same module or core: shown                                   |
| Action plan (Risk) | Risks, controls             | `GET /v1/action-plans/{id}` `linkage` | Same module or core: shown                                   |
| Risk (Risk)        | Controls                    | `linked_control_ids`                  | Core: shown                                                  |
| Policy (Policies)  | Controls                    | `linked_control_ids`                  | Core: shown                                                  |

The reverse directions (policy → control, risk → control) all point
into core. The control-page link reads were already refused by #2172's
gate because the registry places `/v1/controls/*/policies`,
`/v1/controls/*/risks` and `/v1/controls/*/action-plans` under the
module whose data they return; the integration test pins it, and the web
change stops the page asking.

The stripping uses `moduleaccess.Views` (`For`, then `Sees(route)`), the
#2173 helper, so a record names the module it links into by a route the
registry places and keeps no second module map. A failed access read is
a 500, never an unfiltered answer. An empty list says nothing about how
many links were left out ("no hidden-items count").

### D2 — the owner flag is computed per read from live access

`owner_lacks_access` is set on vendors (Third-party risk), risks and
action plans (Risk), on the list and the single read. It reads the
owner's live `user_roles` and `member_module_access` at read time
(`Views.UserHas`, memoised per owner for one response by
`OwnerCheck`), so narrowing or restoring access changes the flag without
touching the record. Nothing is stored and the assignment is unchanged.

(Superseded in part by D5, #2501: a person with no live role is now
flagged and left out of pickers. The paragraph below records the #2174
call as made.)

An owner lacks the module only when an admin narrowed their module
access to leave it out and their live `user_roles` hold neither admin
nor auditor (both exempt, as in `Gate`); no stored row is "all". A
person with no `user_roles` row is not flagged: roles carried only in a
credential (the e2e test token, an IdP claim) cannot be read for someone
who is not the caller, and the first CI run of this PR showed the e2e
tenant's users have no role rows, so treating "no row" as "no access"
emptied every owner picker. The flag and picker are advisory; `Gate`
still refuses the person on their own requests. Whether the module is
switched on is not asked: a switched-off module's records reach no one.

The flag is present only when true (`omitempty`), so the risks list
contract golden is unchanged and an older client reads nothing new.

Policies carry owner and approver roles, not people, so there is nothing
to flag. Exceptions, questionnaires and personnel-security records have
no user-id owner field on the wire today.

### D3 — assignee pickers filter on the server

`GET /v1/people?module=<key>` leaves out the users who cannot work in
that optional module by D2's rule (`Views.MembersWithout`: one read of
stored access, and of live roles only when someone is narrowed out). A
core key filters no one; an unknown key is a 400. API
keys are listed unchanged: they are not assignees. The web owner pickers
on vendors (`third_party_risk`), risks, the risk owner panel and action
plans (`risk`) pass the module through `EntityPicker`'s `module` prop and
the `/api/people` BFF, which forwards that one parameter.

Writes are not refused for an owner without access: the ticket asks that
pickers not offer them, and an existing owner who loses access is kept
and flagged (D2). Refusing the write would make an admin unable to
re-save a record whose owner was narrowed.

### D4 — the web reads modules once, in the authed layout

`(authed)/layout.tsx` reads `GET /v1/me/modules` server-side and provides
it to client pages through `ModulesProvider`; `useSeesPage(page)` answers
from the registry's page ownership (`seesPage`, pure and unit-tested).
Unknown modules (the read failed) fail closed, as `FeatureGate` does. A
component rendered outside the provider sees everything; the API still
refuses.

On the control page, a hidden module's tab, overview card and count are
not rendered and its query is not issued; `?tab=policies` without access
falls back to the overview.

## #2501 follow-ups (review of #2488)

- **detection_tier_actual:** manual_review (the #2488 review). With the
  #2501 server change reverted, four integration tests fail: the
  role-less owner is not flagged, the picker lists the role-less user,
  a narrowed caller gets 200 from `?module=`, and incident create
  answers 201 for an existing hidden vendor but 400 "linked target does
  not exist" for a missing one, the existence oracle.
- **detection_tier_target:** integration.

### D5 — no live role lacks every optional module

D2's "no row is not flagged" rested on roles that live only in a
credential. Since #2487/#2496 a user session decides on live
`user_roles` alone, so a person with no live role cannot use any
module on their own requests; offering them as an owner hands a record
to someone who cannot open it. `moduleaccess.memberLacks` is now the one
rule for the flag and the picker: a super-admin (the `super_admins`
grant on an active user of the tenant, exempt as in `ForCaller`) or a
live admin/auditor has the module; anyone else with no live role lacks
it; anyone else with a live role follows their stored access.
`MembersWithout` now walks the tenant's `users` rows rather than the
stored narrowings, so a role-less user who was never narrowed is left
out too.

The e2e fixtures that seed the shared e2e user
(`44444444-…-0001`: `dashboard.sql`, `audits-header.sql`,
`controls-list.sql`) now also seed its admin and grc_engineer rows, as
`settings.sql` already did, and `person-names.sql` gives its plan owner
a grc_engineer row. The integration-fixture sweep is #2507's.

### D6 — incident create refuses links into a hidden module

`POST /v1/incidents` answers 403 `risk_ids: links into a module you
cannot use` (or `vendor_ids: …`) when the caller cannot see Risk or
Third-party risk and the list is non-empty. Refuse rather than ignore:
silently dropping links a caller sent would create an incident that is
not what they asked for, and nothing in the answer would say so. The
check runs before the store and names only the field, so an id that
exists and one that does not get byte-identical answers; the store's
"linked target does not exist" 400 is reached only by callers who can
see the module. The same `Viewer.Sees` route check the read path uses
decides it, so read and write cannot disagree.

### D7 — `?module=` is limited to callers who can work in the module

Any signed-in role could compare `/v1/people` with `/v1/people?module=X`
and learn who had been narrowed out of X. Restricted, not accepted:
`?module=X` now answers 403 unless the caller can work in X
(`Viewer.SeesModule`: in their access and switched on; core modules
always answer). That set is exactly the people who already see
`owner_lacks_access` on X's records, so the picker discloses nothing
those pages do not. Limiting by role (admin, grc_engineer) was
considered and not taken: which roles assign owners differs per module
and lives in Rego, and a second copy of it here would drift. The
residual, accepted: a member of X can still learn who else cannot use
X, which they need to assign owners at all.
