# #2149 — "How this works" help for the Program section: decisions log

detection_tier_actual: none
detection_tier_target: none

Ticket: https://github.com/mgoodric/security-atlas/issues/2149 (parent #2125).
Follows the Controls (#2150) and Risks (#2151) help slices for front
matter, `<Term>` placement, hint migration and Playwright shape.

## D1 — Five modules, one per sidebar entry

`dashboard`, `approvals`, `calendar`, `metrics`, `activity`. The full
Needs attention list (`/needs-attention`), reached by the dashboard's
**See all**, is covered by the dashboard module instead of getting its own:
it is the same list, and the sidebar has no entry for it. A metric's detail
page (`/dashboards/metrics/{id}`) falls under the metrics module through the
route prefix.

## D2 — No lifecycle diagrams

None of these pages owns a lifecycle in `docs/lifecycles/`. Approvals lists
records from four lifecycles (exception, risk treatment, policy, framework
scope), and each of those is already drawn on its own module's help.
Copying all four into the Approvals panel would duplicate them and bury the
second-person rule, which is the concept the page needs. So no Program
module embeds a diagram, and the Playwright spec and `modules.test.ts`
assert that each panel shows none. The diagram check runs over every help
file in the same suite and still passes.

## D3 — Who can do what, and where each claim comes from

| Claim in the help                                                                                                                   | Code path                                                                                                                                                                                                    |
| ----------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Approvers are admins and GRC engineers; only a person approves                                                                      | `internal/authz/roles.go` `IsTenantApprover`; `internal/api/approvals/handler.go` (role gate, `approver.UserID == ""` → empty list)                                                                          |
| The requester cannot approve their own record unless Allow self-approval is on; then it is marked self-approved                     | `internal/approval/approval.go` `Check` and `approval_test.go`; per type `exception.CheckApprover`, `risk.CheckAcceptanceApprover`, `policy.CheckApprover`, `frameworkscope.CheckApprover`                   |
| Non-approvers see an empty Approvals page                                                                                           | `internal/api/approvals/handler.go` package doc                                                                                                                                                              |
| Only admins change Allow self-approval                                                                                              | existing `settings.md` / `self-approved` term (#2128, #2101)                                                                                                                                                 |
| For you vs Everyone; Everyone adds ownership gaps; only admins and GRC engineers switch                                             | `internal/api/needsattention/rank.go` package doc; `web/app/(authed)/dashboard/page.tsx` (`is_approver`)                                                                                                     |
| Posture target on a framework tile: admins and GRC engineers                                                                        | `internal/authz/tenant_roles_2428_test.go` (`tenantApproverGate` on PUT posture-targets); `web/lib/api/posture-targets.ts` `canSetPostureTarget`                                                             |
| Metrics read: admins and GRC engineers                                                                                              | `policies/authz/*.rego`: `metrics` is not admitted for viewer, control_owner or auditor; admin and grc_engineer read everything                                                                              |
| Metric targets and manual values: admins                                                                                            | `internal/api/metrics/handlers.go` `CreateInput`, `UpsertTarget` (`authz.IsTenantAdmin`); `web/lib/api/metrics.ts` `canSetMetricTarget` (#2341)                                                              |
| Calendar and its subscription: every role, narrowed to readable types                                                               | `policies/authz/{viewer,control_owner,auditor,grc_engineer}.rego` `calendar` admits and the `/v1/calendar/subscription` carve-outs; `calendar.ReadableTypes`                                                 |
| Activity: GRC engineers, auditors and super admins see all; others see shared kinds plus their own rows; feature-flag events hidden | `internal/api/adminauditlog/activity.go` package doc; `internal/db/queries/unified_audit_log_role.sql`; `web/lib/api/audit-log-access.ts` (#2216: a tenant admin grant does not reach the privileged branch) |
| Activity export: same three                                                                                                         | `web/lib/api/audit-log-access.ts` `canReachAuditLog`; `web/app/(authed)/activity/page.tsx`                                                                                                                   |

The Approvals text stays generic on policy audiences (#2524 in flight): it
says each record "also follows its own rules, which its page explains".

## D4 — Terms

Eight new user terms: `needs-attention`, `approver`, `second-person-rule`,
`calendar-subscription`, `board-kpi`, `metric-cascade`, `metric-target`,
`activity-log`. Each module's other terms already existed (`posture`,
`evidence-freshness`, `residual-risk`, `owner`, `self-approved`,
`audit-period`, `exception`, `review-cadence`, `tenant`).

Each page names its terms in its header subtitle, as the earlier slices
did. The dashboard's subtitle is the live freshness figure, so there the
terms go in the panel titles instead (Needs attention, Framework posture
already had one, Top risks by residual, Evidence freshness), which render
in every panel state. Some subtitles were reworded slightly to carry the
term in a natural place (Approvals now names the second-person rule;
Calendar mentions subscribing; Metrics mentions targets; Activity names
the activity log).

## D5 — The "no help module" checks move to a requirement page

`/calendar` now has help, so `help-panel.spec.ts` and `modules.test.ts`
use `/requirements/{id}`, which no module covers. The Playwright check uses
an all-zero id; the page renders its "Requirement" header whatever the
fetch returns.

## D6 — Hints

The five ids join `userprefs.HintModules`, and migration
`20261254000000_onboarding_hint_program_modules.sql` widens the dismissal
CHECK, so the three lists stay in step (`modules.test.ts` pins this).
