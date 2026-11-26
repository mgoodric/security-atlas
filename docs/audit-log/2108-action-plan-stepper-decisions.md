# #2108 — action-plan stepper and edit — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2108
**Parent:** https://github.com/mgoodric/security-atlas/issues/2071
**Date:** 2026-11-16

The action-plan detail page moves to the shared DetailHeader, with a button for
each transition the state machine allows, an Edit route, and a manual Verify for
plans with no linked controls.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: none
- `detection_tier_target`: none

---

## D1 — Verify has its own route, and PATCH no longer sets `verified`

`POST /v1/action-plans/{id}/verify` is the only way a person verifies a plan.
`PATCH` with `status: "verified"` returns 422 for everyone. Left on PATCH, the
owner rule would have to be re-checked on a route whose role gate is the looser
program-write signal, and any missed path would let an owner verify their own
plan. One route, one check.

## D2 — the owner never verifies, even with Allow self-approval on

The store calls `approval.Check(owner, approver, false)`: self-approval is passed
as off, whatever the organisation's setting. The ticket says "never the plan's
owner", and the parent spec scopes the self-approval opt-in to exceptions, risk
acceptances, policies and framework scopes. A machine credential (no person
behind it) is refused, as `approval.Check` refuses it everywhere. The approver
role is `IsApprover || IsAdmin`, as on the risk transitions.

## D3 — a plan with linked controls is refused with 409, not hidden only

Its controls' evaluations verify it (#2109). Until #2109 lands, such a plan
stays Completed; that is the intended trade, since a hand verification is what
made "Verified" a label that checked nothing. Verify on a plan that is not
Completed is 422, the module's existing status for an illegal transition.

## D4 — the header carries the forward step; the overflow carries the rest

DetailHeader has one primary action and an overflow menu. The primary is the
forward step (Start, Mark complete, Unblock, Verify); Block, Mark complete from
Blocked, and Reopen go in the overflow. Every transition is offered, each
confirms inline, and each toasts. A small stepper under the title shows where
the plan sits, with Blocked shown as a side state of In progress.

The state machine's `blocked -> in_progress` action is named `resume` in the
lifecycle table; the button reads "Unblock", as the ticket names it.

## D5 — the page shows Verify from `/v1/me`

`is_admin` is the approver signal for a signed-in person (the JWT bridge sets
`IsApprover` from the super-admin flag), so the page shows Verify when the
viewer is admin and is not the owner, comparing bare uuids. The platform makes
the same checks again; the page only decides what to offer.

## D6 — an owner must be an active user

`ActionPlanOwnerExistsInTenant` now also requires `status = 'active'`, on create
and edit alike, so a plan cannot be handed to a disabled user. A cross-tenant id
was already refused (RLS and the tenant predicate).
