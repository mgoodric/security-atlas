# 3057 — Board brief exception aggregate honours as_of: decisions log

Issue: mgoodric/security-atlas#3057 (from the #3054 review of #3037).

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #3037; no caller
  passed a past `as_of`, so nothing was ever wrong in a generated brief.
- **detection_tier_target:** integration. `TestIntegration_ExceptionAggregate_AsOfPastInstant`
  now pins it, and was observed failing against the old query
  (`{ActiveCount:2 PastDueCount:0 OldestActiveAgeDays:10}`, want `{3 1 50}`).

## Decisions

### D1 — rebuild "status was active at as_of" from the lifecycle stamps

`BoardBriefExceptionAggregate` filtered on the current `status = 'active'`,
so only `past_due_count` used `as_of`. It now selects rows with
`activated_at <= as_of`, `revoked_at` NULL or after `as_of`, and `expired_at`
NULL or after `as_of`, and drops the status predicate.

This is exact rather than approximate because each transition into or out of
`active` stamps a column: `ActivateException` sets `activated_at`,
`RevokeException` sets `revoked_at`, `ExpireActiveExceptionsBefore` sets
`expired_at`. At `as_of = now`, which is what the only caller passes
(`internal/board/generator.go`, slice 751 D2), the selected set is today's
`status = 'active'` set, so the current brief is unchanged. The four existing
aggregate tests pass unchanged.

### D2 — not the [effective_from, expires_at) window

The ticket and the dispatcher suggested the window that
`ListExceptionsByControlAsOf`'s evaluator and `oscal/excepted.go` apply. That
window answers a different question, whether the waiver suppressed evaluation
at t. The board aggregate reports lifecycle status, and its
`past_due_count` exists to show waivers still active after their
`expires_at` (sweeper lag, slice 751 D1). Under the window that count would
always be 0, and an exception activated with a future `effective_from` would
leave `active_count`, so today's brief would change. The activation/revoke
half of the rule is shared with `ListExceptionsByControlAsOf`; the
`expired_at` term replaces the window.
