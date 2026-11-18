# #2457 — approvals list: route-ownership guard, filtered reads, tenant in the key: decisions

detection_tier_actual: manual_review
detection_tier_target: unit

Source: the review of #2453 (#2119). Three follow-ups on GET /v1/approvals
and its sidebar badge.

## D1 — the ticket's fail-open premise is out of date; the guard is still worth having

The ticket says `featureflag.RouteOn` fails open when `Resolve` errors.
That was true when #2453 merged. #2579 (#2352) has since changed it: only
`ErrNoModule` reads as "on", and any other `Resolve` error (two owners
tied) is returned, so `Pending` answers 500 rather than listing.

`ErrNoModule` still reads as "on", and that is the gap left for this
list. A renamed approve route whose `approveRoutes` entry was not updated
would resolve to no module, and its type would be listed with its module
switched off. `TestApproveRoutes_ResolveToOwningModule`
(`internal/api/approvals/routes_test.go`) closes it. Every entry must
name a registered `PATCH` route in `internal/api/testdata/routes_modules.golden`
(the #2352 golden that every registered route is held to), and
`Resolve` must answer that route's owner from the golden, with no error.
`ErrNoModule` fails the test. Checked red by renaming the policy entry
to `/v1/policy/_/approve`.

## D2 — four filtered row reads, not COUNT queries

The handler now reads `ListRequestedExceptions`,
`ListRisksWithProposedAcceptance`, `ListPoliciesUnderReview` and
`ListFrameworkScopesInReview` in place of the four tenant-wide `List*`
loads. Each filters in SQL on the state the approve move leaves from,
and runs on the app pool under the caller's RLS tenant.

The dispatcher asked for `COUNT` queries for the badge. They would not
give the same number. A row is counted only when the type's own approver
check admits the caller (`exception.CheckApprover` and the others): the
requester's own row is left out unless the organisation allows
self-approval, and a row whose requester cannot be compared is left out.
Those functions run in Go, inside the approve transaction, and the
package's contract is that the list and the approve handler cannot
disagree. A SQL `COUNT` would have to restate that predicate. It would
drift, and the badge would then show a row the page does not list. The
badge and the page share one response (`counts`, `total`), so the badge
now reads only pending rows. That is what the 60-second refetch costs.

The Go `Pending*` filters keep their state check. It is redundant with
the SQL now, and costs one comparison per row. It keeps them correct for
a caller that hands them a wider slice, as the unit tests do.

## D3 — no new index

| Query                             | Index                                                                          |
| --------------------------------- | ------------------------------------------------------------------------------ |
| `ListRequestedExceptions`         | `idx_exceptions_tenant_status_expires (tenant_id, status, expires_at)`, prefix |
| `ListPoliciesUnderReview`         | `(tenant_id, status, created_at DESC)`                                         |
| `ListFrameworkScopesInReview`     | `(tenant_id, framework_version_id, state)`, tenant prefix                      |
| `ListRisksWithProposedAcceptance` | several `(tenant_id, …)` indexes, tenant prefix                                |

Every query is bounded to one tenant's rows by a `tenant_id`-leading
index. Risks and framework scopes then filter within that tenant, a few
hundred rows at the canvas's scale (§1.4). A partial index on `risks`
for `acceptance_status = 'proposed'` would need a migration for a filter
over that small a set. It can be added if a tenant's risk register grows
by orders of magnitude.

## D4 — tenant in the key, the #2399 pattern

`approvalsQuery(tenantId)` keys the list `["approvals", "pending", tenantId]`
and is disabled while the tenant is unknown. The page and the badge read
the tenant from `useSessionTenant()` (`<AppShell>` provides it from the
JWT claim, #2619). `APPROVALS_QUERY_KEY` stays as the prefix, so
`invalidateApprovals` still reaches every tenant's entry and the approval
flows that call it need no change.

The page now shows its loading state on `isPending` rather than
`isLoading`, so a disabled query (tenant unknown) shows "Loading…", as
`<Person>` does, rather than an empty page.

The general tenant-switch cache reset is #2621's, not this ticket's.

## Tests

- `internal/api/approvals/routes_test.go`: unit test, approve route to module (D1).
- `internal/db/approvals_pending_integration_test.go`: each query returns only
  its pending row next to rows in every skipped state, for two tenants, each
  seeing only its own under RLS. Checked red by widening the policy filter
  to `('under_review','draft')`.
- `web/lib/api/approvals.test.ts`: the key carries the tenant; tenant A's
  cached list never reaches an observer for tenant B; unknown tenant reads
  and fetches nothing; `invalidateApprovals` reaches every tenant.
