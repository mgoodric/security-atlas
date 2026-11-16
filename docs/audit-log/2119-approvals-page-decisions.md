# #2119 — Approvals page and sidebar badge: decisions

detection_tier_actual: none
detection_tier_target: none

No bug surfaced. A deliberate mutation (the exception filter ignoring the
self-approval setting) failed the integration test before it was
reverted, so the test is known to detect a list that drifts from the
approve gate.

## D1 — the list calls each type's own approver check, exported

The rule is "list exactly what the approve handler would accept". Each
store already had an unexported checker that `Approve` calls inside its
transaction (`exception.checkApprover`, `policy.checkApprover`,
`frameworkscope.checkApprover`, `risk.checkAcceptanceApprover`). They
are now exported, and `internal/api/approvals` calls them once per row
with the same `approval.Approver{UserID: MinterUserID, CredentialID:
cred.ID}` the handlers build. No predicate is restated. The gates the
handlers apply before the store are applied in the same order:

| Gate                                            | Where it comes from                                       |
| ----------------------------------------------- | --------------------------------------------------------- |
| module on                                       | `featureflag.RouteOn` (the ModuleGate check)              |
| approver role                                   | `authz.IsTenantApprover`                                  |
| prior state                                     | `requested` / proposed + open / `under_review` / `review` |
| second person, self-approval, legacy requesters | the exported checker                                      |

A closed risk with a proposed acceptance is not listed: `lockOpenRisk`
refuses it until the risk is reopened.

`featureflag.RouteOn` is new. It is `ModuleGate`'s own `switchedOffKey`
behind a path, so a switched-off module lists nothing rather than links
that would 404.

## D2 — every signed-in role reads it; the answer is per caller

The OPA admit (`defaults.rego`) is the exact path for any role, like
`/v1/people`. The sidebar asks before it knows the caller's roles, and an
empty list is the honest answer for a viewer. A machine credential gets
an empty list (200), not 403: only people approve (#2101), and the
dispatcher brief allowed either. The sidebar entry shows for every role;
the badge renders only on a non-zero total.

## D3 — counts are full, items are capped

Counts and `total` cover every pending row. Items are capped at 100 per
type, oldest first, with `truncated` set. The page says "Showing the
oldest N of M" when a type is cut. The four source lists are the existing
tenant-wide sqlc reads (`ListExceptions`, `ListRisks`, `ListPolicies`,
`ListFrameworkScopes`), filtered in Go. They return full rows, which is
what the exported checkers take. Their size is bounded by tenant size, as
their own comments note.

## D4 — framework scope `requested_at` is `updated_at`

`framework_scopes` records `submitted_by` but no submission time. The
last update stands in for it, for ordering and display only.

## D5 — one query for the badge and the page

The badge (`ApprovalsCountBadge`) and the page share
`APPROVALS_QUERY_KEY`. The page refetches on every mount. The four
approval flows (exception detail and action pages, the policy page, the
risk acceptance form, the framework-scope page) invalidate it on any
transition, so the badge drops without a reload. The route lives in the
core Dashboard module (Program group), so it has no module switch of its
own.
