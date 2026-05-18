# 129 — Extend slice-124 `/v1/admin/audit-log/unified` with `actor_name`

**Cluster:** Backend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Spillover from slice 125 (frontend `/audit-log` page). Filed 2026-05-18 by
the slice 125 implementing engineer.

The slice-124 endpoint returns `actor_id` (a UUID) but no `actor_name`.
The slice-125 page therefore renders a truncated 8-character UUID prefix
in the actor column — operator-hostile.

The two alternatives the slice 125 engineer considered (see slice 125
decisions log D1) were (a) extend the backend with `actor_name`, OR
(b) per-row client-side resolution. (b) is an N+1 fan-out at the BFF
(1000 rows × 1 round-trip = unacceptable). This slice ships (a) so the
page can render human-readable actor names.

The join is a LEFT JOIN onto `users` (or the analogous tenant-scoped
identity table) keyed on `actor_id`. Bootstrap-key callers and
credential-only callers have no `users` row — the wire shape MUST tolerate
`null` and the frontend renders `actor_id` truncated as today's fallback.

## Threat model

| STRIDE                       | Threat                                                                 | Mitigation                                                                                                                          |
| ---------------------------- | ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a — read-only join, no new auth surface                              | n/a                                                                                                                                 |
| **T** Tampering              | n/a — read path                                                        | n/a                                                                                                                                 |
| **R** Repudiation            | n/a — meta-audit unchanged                                             | inherits slice 124's meta-audit                                                                                                     |
| **I** Information disclosure | LEFT JOIN onto `users` leaks Tenant A's user display names to Tenant B | Same `tenancy.ApplyTenant` transaction as the aggregator query — RLS on `users` keeps the join tenant-scoped (verified by ISC test) |
| **D** Denial of service      | LEFT JOIN slows the aggregator query                                   | Indexed join (`users.id` is the primary key); EXPLAIN ANALYZE check in the integration test                                         |
| **E** Elevation of privilege | n/a — same role gate as slice 124                                      | n/a                                                                                                                                 |

## Acceptance criteria

- [ ] AC-1: `internal/audit/unifiedlog/Entry` gains an `ActorName string`
      field. Wire JSON tag `actor_name` (snake_case to match the rest of
      the response).
- [ ] AC-2: The aggregator SQL query LEFT JOINs onto `users` via
      `actor_id::uuid = users.id` and projects `users.display_name` (or
      whichever column holds the human-readable name). Rows where the
      LEFT JOIN finds nothing emit `actor_name = ""` (empty string is
      the contract — null is also acceptable; document the chosen shape
      in D1 of the decisions log).
- [ ] AC-3: ISC integration test (`unified_integration_test.go`) asserts
      Tenant A's request sees only Tenant A's user names; Tenant B's
      names are invisible. Reuses the existing tenant-isolation harness
      from slice 124.
- [ ] AC-4: Integration test asserts a credential-only caller (no `users`
      row) returns `actor_name=""` (or `null`) rather than failing.
- [ ] AC-5: EXPLAIN ANALYZE in the integration test confirms the LEFT
      JOIN uses an index path (no Seq Scan).
- [ ] AC-6: Frontend follow-up: slice 125's table cell upgrades to
      `{actor_name || truncated actor_id}`. One-line change to
      `web/app/audit-log/page-client.tsx`. Lands in the same PR as the
      backend change.
- [ ] AC-7: Decisions log at
      `docs/audit-log/129-audit-log-actor-name-backend-extension-decisions.md`
      records the JUDGMENT calls (empty-string vs null, which display
      column to use, etc.).

## Dependencies

- **124** (unified audit-log aggregation API) — merged.
- **125** (frontend `/audit-log` page) — should land first so AC-6 is a
  pure upgrade.

## Anti-criteria (P0)

- **P0-A1**: Does NOT change the role gate or the tenant-isolation
  posture inherited from slice 124.
- **P0-A2**: Does NOT join on a denormalized column — `actor_id` is the
  stable identifier; the join goes to the canonical `users` table only.
- **P0-A3**: Does NOT break callers of the existing wire shape. The
  `actor_name` field is purely additive.

## Notes

The slice 125 PR ships with a TODO marker in
`web/app/audit-log/page-client.tsx` near the actor-truncation logic so
the slice 129 follow-up is discoverable from the code path.
