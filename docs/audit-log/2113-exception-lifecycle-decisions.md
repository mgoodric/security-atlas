# 2113 — Exceptions in the app: request, approve, activate, revoke: decisions log

Issue: mgoodric/security-atlas#2113 (parent #2071). "Request exception" on a
control opens `/exceptions/new?control_id=…`; `/exceptions/{id}` carries the
exception through approve, deny, activate and revoke; a new
`PATCH /v1/exceptions/{id}/revoke` ends an active exception early.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced during the work.
- **detection_tier_target:** none.

## Decisions

### D1 — revoked is a new terminal state, not a reuse of expired

Revoke could have set `expired` early. It does not: an auditor reading the
trail must tell an exception that ran its course from one an approver cut
short, and the reason belongs to the revoke. `revoked` joins the status and
audit-log CHECKs (migration `20261118000000_exception_revoke`), with
`revoked_by` / `revoked_at` on the row and a CHECK that a revoked row names
both. `expires_at` is left as granted, so the record shows the approved
window and when it ended. The down migration refuses while revoked rows
exist, as #2009 does for audit evidence.

### D2 — who may revoke: the approver role, no segregation-of-duties check

Revoke follows activate: `admin` or `grc_engineer` in the rego bundle, and
the handler's approver flag. It carries no approver ≠ requester check,
because revoking narrows what is waived; an approver who requested an
exception may withdraw it. A reason is required, at the handler and the
store, and is written as the audit row's reason.

`grc_engineer` does not hold `revoke` in `grc_actions`, where it would also
admit revoking keys and grants; the new rego rule admits it on `exceptions`
only (`internal/authz/exception_revoke_2113_test.go`).

### D3 — a null scope predicate is refused at the form, the client and the API

The #2290 review found that the scope picker's `null` means "incomplete"
while `internal/scope` reads a stored null as every cell. Three layers:

1. `validateRequest` builds no body for a null predicate, and the submit
   button is disabled while the picker reports null;
2. `createException` throws before any request for a predicate that is not
   an object;
3. `POST /v1/exceptions` answers 400 for a predicate that is JSON `null` or
   not an object. An omitted predicate still stores `{}`, as before.

The whole control is sent as `{"op":"true"}`, the picker's Everything.

### D4 — which actions get a route

Per the parent's detail-page pattern: approve takes no input and confirms
inline on the detail page; deny (reason), activate (optional effective-from
day) and revoke (reason) each open `/exceptions/{id}/{action}`, one page
file for the three. There is no Edit: an exception is never changed after it
is requested (canvas §6.3).

The UI requires a reason to deny although the API does not; the parent asks
for "deny (with a reason)".

### D5 — the page offers actions; the platform decides

As on the audit-period page (#2117), the detail page offers each state's
action to everyone and shows the platform's refusal (403 for a role or the
approval rule, 409 for a wrong state) as a toast or an inline error. It does
not try to hide Approve from the requester, which would need the caller's
identity and the organisation's self-approval setting on the client.

### D6 — the Playwright spec mocks the exception BFFs

The spec drives the real seeded control page and controls list, and mocks
`/api/exceptions*` on two browser contexts that share one in-memory store,
one context per person. Approving or revoking a real exception would change
shared seed state for every other spec. The rule itself is covered against
Postgres in `internal/exception` (`TestHTTP_ExceptionRevoke_*`,
`TestHTTP_ExceptionApproval_FollowsSelfApprovalSetting`).

### D7 — justification renders as plain text

The Markdown component (#2313) is still in review. Justification renders as
plain text with line breaks kept; switching it to the component is a
one-line change once that lands.
