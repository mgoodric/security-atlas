# #2329 — LLM settings audit follow-ups — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2329
**Date:** 2026-11-17

Follow-ups from the review of #2326 (#2224): attribute machine-key changes,
record an exact before value on routing changes, and make rerank slots fair
between tenants.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

All three were found in review of #2326. The before-value race and the
cross-tenant failure are concurrency defects an integration or unit test
with a forced interleaving would have caught; the tests added here force it.

---

## D1 — the acting credential is a column, `me_audit_log.actor_principal`

`me_audit_log.user_id` is a users(id) uuid, so a change made with a key that
is not a user's (the bootstrap admin, an unbound API key, an OAuth client)
was written as `uuid.Nil` and named nobody. Migration `20261252000000` adds
a nullable `actor_principal TEXT` (1 to 256 characters when set). It holds
`credstore.PrincipalOf(cred)`: the bare users(id) uuid for a person, else
the key id (`key_...`), `oauth_client:<id>` or `bootstrap-admin`. That is the
value `control_evaluations.triggered_by` holds (#2479), so the two records
name an actor the same way. It is an id, never a token or a hash of one.

A column rather than a field in the after value:

- The after value's shape is per action (routing has `provider` /
  `has_api_key`, the tenants handler has one key per setting). An auditor
  asking "what did this credential change" would have to know every shape.
- The routing after value is deliberately closed to four fields (#2224 D2),
  and its integration test fails on any other field.
- One column serves every writer of `InsertMeAuditLog`. This change sets it
  on the three LLM settings actions and on the five tenants handler actions
  (rename, gate policy, risk review cadence, self-approval, vendor initial
  review window). Other writers pass NULL until they adopt it; rows from
  before the migration stay NULL.

The tenants handler takes the principal from the credential and falls back
to the JWT subject, matching how it already resolves `user_id`.

## D2 — routing writes take a per-tenant advisory lock, then `FOR UPDATE`

`cloud.Store.Set` and `Clear` now start their transaction with
`LockTenantLLMRouting` (`pg_advisory_xact_lock(9, hashtext(tenant))`,
namespace 9 registered as `advisorylock.TenantLLMRouting`), then read the
before value with `GetTenantLLMRoutingForUpdate`.

`FOR UPDATE` alone, as `controlimport.Store.SetSettings` uses, is not
enough here: a tenant on the default has no `tenant_llm_routing` row, and
`FOR UPDATE` on no row locks nothing, so two first PUTs would each record
the default as their before value. The advisory lock serialises every
routing write in a tenant whether or not a row exists. The row lock is kept
as the read the ticket asks for, and it costs nothing once the advisory lock
is held.

The integration tests hold the lock themselves until both writes are queued
on it (checked in `pg_locks`), then release it, so the race happens on every
run: two concurrent first PUTs, and a PUT racing a DELETE. Each asserts that
the two audit rows chain: one row's before is the starting state and the
other's before is the first row's after. With the lock call removed, both
tests fail.

## D3 — rerank slots are shared fairly, and a busy process defers, not fails

Supersedes #2224 D5. D5 said a busy tenant "delays another tenant's imports
but cannot hold a slot forever". That was wrong: an inline run waited on the
request's context, and when that ended first the run marked its batch
**failed**. So one tenant's reranks could fail another tenant's import.

Two changes in `Stager`:

1. **Fair slots** (`runSlots`, `internal/controlimport/slots.go`). The cap
   stays `MaxConcurrentSuggestionRuns` (4) per process. When a slot frees it
   goes to the waiting run whose tenant holds the fewest slots; ties go to
   the tenant served least recently, then to the longest-waiting run. A
   tenant queued behind another tenant's backlog takes the next slot that
   frees. With one slot, waiting tenants take turns.
2. **Defer instead of fail.** An inline run waits at most `InlineSlotWait`
   (2s) or until its request context ends. If no slot is granted, the batch
   is returned `pending` and suggested in the background, which waits for a
   slot without a deadline. A batch fails only for its own errors, never for
   another tenant's load.

Alternatives considered:

- **A per-tenant cap (for example n-1 slots).** Rejected. Most deployments
  are single-tenant self-hosts, and a cap would leave a slot idle for them.
  The fairness order already sends the next free slot to the other tenant;
  the cost is that tenant B can wait up to one of tenant A's in-flight runs,
  which the rerank budgets bound.
- **Per-tenant queues with strict round robin.** Equivalent to the order
  above when every tenant holds the same number of slots. Ranking by slots
  held also covers the uneven case without a second structure.

A background run that waits past the sweeper's cutoff is still failed by the
sweeper, and the run then drops itself (`ErrSuggestionsNoLongerPending`), as
before. With fair slots that wait is bounded by other tenants' in-flight
runs rather than by their backlog.
