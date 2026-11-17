# 2134 — Needs-attention endpoint: decisions log

Issue: mgoodric/security-atlas#2134 (parent #2072, front-door redesign).
After #2119 (approvals), #2130 (the calendar as the one list of what is
due), #2129/#2128 (person owners on risks, vendors and controls) and #2173
(combined views follow module access).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced. A mutation that
  counted every item as the caller's made `TestNeedsAttention2134_ForMeAndForAll`
  fail with nine unexpected items; restored, all five integration tests pass.
- **detection_tier_target:** integration (the production router, real
  Postgres under RLS, the live user_roles resolver).

## Decisions

### D1 — each band reads an existing query

No band re-derives its rule:

| Band          | Source                                                                                   |
| ------------- | ---------------------------------------------------------------------------------------- |
| failing       | `ListActiveControlsWithState`, the controls list's query; rolled-up state `fail`         |
| approval      | `approvals.Handler.Pending`, the body of `GET /v1/approvals`, exported for this          |
| overdue       | `calendar.Store.ListEvents`; the calendar's `overdue` status                             |
| due_soon      | the same events; the calendar's `due-soon` status, and within 14 days (`duedate.Status`) |
| ownership_gap | the controls query's `owner_user_id IS NULL`; `ListRisks`, open with no `owner_user_id`  |

Owners of the due-based items are read by id from the owning table
(`risks.owner_user_id`, `vendors.owner_user_id`, `action_plans.owner_id`,
`control_owner_assignments`, `policies.owner_role`). Those are lookups,
not rules, so they are inline SQL as `approvals.controlTitles` is.

### D2 — "in scope" is the controls list's set

The spec says "failing in-scope controls" and "unowned in-scope controls"
without defining scope. They are the controls the controls list shows:
active (`lifecycle_state = 'active'`) and not superseded. A draft or
retired control needs no owner and cannot fail an audit. Framework-scope
membership is not used: a control outside every framework scope is still
one the program runs.

### D3 — due-soon is 14 days for every type

A policy review is `due-soon` on the calendar 30 days out
(`policy.ReviewDueSoonDays`). Band 4 is "due within 14 days", so a policy
review is listed there only when the calendar says due-soon and it falls
within 14 days. The overdue band takes the calendar's `overdue` status
as is, for every type.

### D4 — an exception is rated by its expiry date

An exception's calendar status is its lifecycle state (`active`), not a
due status. Its expiry is rated with `duedate.Status` at 14 days, the rule
the calendar's store applies to every other dated type: expiring within
14 days is due soon, and an active exception whose expiry has passed (the
sweeper has not run) is overdue.

### D5 — the calendar sources band 3 and 4 read

Policy reviews, risk reviews, vendor reviews, action plans, periodic
control reviews and exception expiries. Left out: audit-period ends (the
audit banner counts those down), policy acknowledgement lapses (one row per
policy and day across many people; nobody owns it), offboarding checklists
(personnel-security work with its own page), and vendor contract renewals
(a commercial date, not a review; the row shares the `vendor` type and is
told apart by its `renewal-` event id).

### D6 — owners

- A control's owner is its person assignment (`control_owner_assignments`).
- An exception's owner is its control's owner, who renews it or lets it
  lapse.
- A policy's owner is its owner role. It is the caller's when the role is
  in `authz.AckRoleLabels(cred)`: live `user_roles`, plus an API key's own
  label. A JWT role claim never counts (#2381). `authz.HasOwnerRole` is not
  used because its admin-flag wildcard would make every policy the
  super-admin's.
- An approval item's owner is the caller, who must act on it.
- A credential with no person behind it owns nothing.

### D7 — who is a "GRC lead"

`authz.IsTenantApprover`: a tenant admin, the approver flag, or the live
`grc_engineer` role, the same set that approves. OPA admits every role to
the path, because `for=me` is anyone's and the view is a query parameter
OPA does not see. The #2428 gate table lists `GET /v1/needs-attention?for=all`
with the control_owner, auditor and viewer pairs as handler-narrower.

### D8 — dates and order

Each item carries `due_at` (due-based bands) or `since` (failing: the
latest evaluation; approval: when it was requested; gap: when the record
was created). A failing control's `since` is its latest failing evaluation,
not when it first failed: the evaluation ledger has the history, but
reading the first failure per control is a second query for a value the
list only uses to order. Order is band, then that date ascending (undated
last), then type, then id, so offset paging has no duplicates or gaps.

### D9 — the default view

With `for` omitted the view is `me`. If the caller has no items and may see
everyone, it is `all` (#2072 user story 4). The response's `for` says which
view was served. Since approvals count as the caller's, an approver with
pending approvals always gets `me`.

### D10 — module filtering

The item types are narrowed twice before any read, as the calendar does:
by OPA read access (`calendar.ReadableTypes`) and by module visibility
(`calendar.VisibleTypes`, over `moduleaccess.Views`). Approval items are
filtered by the approvals handler itself. Hidden items are never read, so
`total` and `counts` cannot count them. A module-access error is a 500.

### D11 — fixture changes

The attention fixture's overdue vendor now has `owner_user_id` set to the
tenant's Owner, and the overdue policy's `owner_role` is `admin` (was the
free-text `security_lead`), so a live role can hold it. No other suite read
either value.
