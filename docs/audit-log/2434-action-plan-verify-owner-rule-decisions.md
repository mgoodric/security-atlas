# #2434 — action-plan verify: owner reassignment, row lock, BFF tests — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2434
**Follows:** #2108 (manual verify), #2507 (live-role gates), #2564 (BFF param guards)
**Date:** 2026-11-18

Review follow-ups from #2423. The owner rule checked only the owner at
verification time; the guard read the plan without a lock; two BFF routes
had no per-route test of their id guard; a test credential was not a person.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

---

## D1 — the owner rule reads the audit trail, and fails closed

`Verify` refuses, besides the current owner, everyone the plan's audit trail
names as having moved it to `completed`, and everyone who owned it at any
point from completion onwards (the `owner_id` in the before or after state of
every row from that point). The reassign -> verify -> reassign-back bypass
is refused even though each step is audited, because the verifier is in the
trail as the owner after completion.

The trail is read in the same transaction as the guard, under the plan's
row lock (D3). The rule fails closed: an error reading the trail, a row
whose state does not parse, a completion row with no person, or a completed
plan with no person's completion row in its trail is a refusal. All map to
403 (`ErrVerifierInvolved`); the wrapped detail is not sent to the client.

## D2 — the window opens at the first completion

"From completion onwards" is read from the plan's first move to `completed`,
not its latest. A plan completed, reopened and completed again keeps everyone
from the first round on the refused list. The wider set is the safe side to
be wrong on, and it does not depend on the precise order of rows written by
transactions that overlapped. The system's `verified -> completed` move
(#2109) is not a completion: it names no person, and the person who first
completed the plan is already on the list.

## D3 — `FOR UPDATE` on every guarded plan write

`Store.update` (PATCH and verify) reads the plan with the new
`GetActionPlanByIDForUpdate`, in the same transaction as the UPDATE. Every
check the guard makes (status, linked-control count, owner, audit trail)
runs after the lock is held. A control link in flight is seen because the
link tables' composite FK takes `KEY SHARE` on the plan row, which
`FOR UPDATE` conflicts with: either the link commits first and verify's
count sees it (409), or verify commits first and the link lands on a
verified plan, where the reconciler (#2109) owns it. The link path itself is
left on the unlocked read; the FK already serialises it against verify.

A concurrent owner change is a PATCH through the same locked path, so it
can no longer slip between the guard and the UPDATE (which writes every
column, and so would have overwritten it).

`TestVerify_GuardReadsUnderLock` forces the interleaving: an uncommitted
link insert, then `Verify` in a goroutine. With the lock, verify waits and
returns `ErrVerifyHasControls` once the link commits. Without it (checked by
reverting the query), verify returns immediately and verifies the plan.

## D4 — `nonApprJWT` is a real grc_engineer, and verify refuses it

The verify env now runs the real OPA bundle with the DB roles resolver
(`liveroletest.Attach`), and the owner and approver hold the live `admin`
role in `user_roles`. `nonApprJWT` is `liveroletest.Bearer(..., "grc_engineer")`:
a person with the live grc_engineer role and no approver flag.

Intended behaviour: **403**. grc_engineer is an approver since #2428, but
verify is an action-plan write, and action-plan writes are tenant-admin only
(#2507, `authz.IsTenantAdmin` in `VerifyActionPlan`; `action-plans` is not in
grc_engineer's writable set in OPA). The role gate refuses it before the
owner rule runs.

## D5 — BFF: the guards were already on `main`; only tests were missing

`web/app/api/action-plans/[id]/route.ts` and `[id]/verify/route.ts` already
call `rejectNonUUID` (#2564), and `bff-uuid-guard.test.ts` covers them in its
repo-wide sweep. This change adds per-route tests: `..`, `.` and a non-UUID
answer 400 with no cookie read and no fetch, and a UUID forwards to the
exact upstream path.
