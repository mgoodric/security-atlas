# Slice 439 — evidence-staleness digest + alerting: decisions log

JUDGMENT slice. The threshold-rollup shape and the digest/alert copy are
subjective calls. Claude made them with pattern-matched judgment, recorded
them here, and the slice ships when CI is green. The maintainer iterates
post-deployment from the "Revisit once in use" list.

- detection_tier_actual: unit
- detection_tier_target: unit

(One bug surfaced DURING the slice — a wrong test assumption about
`time.Truncate`'s epoch-relative 6h boundary in `TestPeriodKeys`, plus the
author's own honest-interval copy tripping the banned-phrase guard. Both were
caught by the package's own pure-Go unit tests before any integration run or
review — `actual=unit`, and `unit` is the cheapest tier that should catch a
copy/period-key logic error, so `target=unit`. No RLS/cross-tenant bug
surfaced; the load-bearing isolation test passed first try.)

## Context that shaped the slice

The slice brief assumed slice 439 would be the FIRST notification-staleness
work and would "write to the /v1/me/notifications store." The tree had moved
on: slices **445** (email channel), **541** (wire staleness digest to email),
**543** (Slack + webhook channels), **566** (per-kind email opt-out events),
**582/583** (digest scheduler + per-kind channel filter) had already landed —
and they all CONSUME a notification type string `evidence.staleness`
(`internal/notify` `typeLabels` + `kindToEvent`, `internal/auth/userprefs`
event `evidence_staleness`). The downstream delivery surface was fully built
and waiting for a producer that never shipped.

So slice 439 is the **missing producer**: the scheduled rollup that writes
`evidence.staleness` notification rows. Everything downstream already routes
them. This re-framing is the single most load-bearing decision and it is
forced by the existing code, not chosen — see D1.

## Decisions made

### D1 — Notification type string is `evidence.staleness` (FORCED, not chosen)

- **Options:** (a) invent a new type string for the producer; (b) reuse the
  `evidence.staleness` string the already-merged channels expect.
- **Chosen:** (b). The string is load-bearing across `internal/notify`
  (`typeLabels`, `kindToEvent`: `evidence.staleness → evidence_staleness`) and
  `internal/auth/userprefs.Events`. A new string would strand the producer —
  the email/slack/webhook digests would never include the rows.
- **Implementation:** `notifications.TypeEvidenceStaleness = "evidence.staleness"`
  with a comment naming the downstream maps that must move with it.
- **Confidence:** high (mechanically verified against the existing maps).

### D2 — "Approaching stale" band width = 14 days, fixed absolute

- **Options:** (a) percentage of the freshness class (e.g. 20% of max-age);
  (b) fixed absolute window.
- **Chosen:** (b), 14 days. A fixed runway is predictable for the solo
  operator regardless of class — a monthly and a quarterly control both warn
  two weeks out, which reads as "you have ~2 weeks to refresh this." A
  percentage makes the quarterly warning fire ~18 days out and the monthly
  ~6 days out, which is harder to reason about and gives the shortest runway
  exactly where evidence is hardest to regather (monthly cadence).
- **Confidence:** medium. The 14-day number is a judgment; revisit once real
  operators report noise/runway feel (see revisit list).

### D3 — Per-control ALERT fires only on the STALE crossing; APPROACHING is digest-only

- **Options:** (a) fire a per-control alert for both stale and approaching;
  (b) per-control alert only on stale, summarize approaching in the weekly
  digest.
- **Chosen:** (b). Approaching-stale is a planning signal, not an incident; a
  per-control ping for every approaching control would be the alert-fatigue
  failure the slice exists to avoid. The weekly digest's "N approaching" count
  is the right altitude for the early warning. The stale crossing IS an
  actionable event and earns a per-control alert.
- **Confidence:** high (matches the measured-tone / honest-volume discipline).

### D4 — Recompute cadence = every 6h; weekly digest = Monday 09:00 UTC

- **Options for recompute:** 1h / 6h / daily. **Chosen:** 6h — frequent enough
  that a solo operator learns of a freshly-stale control the same business
  day, infrequent enough that the alert is plainly periodic (honest-interval
  discipline; P0-439-1). The matching human copy
  (`RecomputeIntervalText = "every 6 hours"`) is the single source the UI +
  payload + docs read from.
- **Digest cadence:** weekly, Monday 09:00 UTC — start-of-week "what do I need
  to fix this week" surface. Stated verbatim
  (`DigestCadenceText = "every Monday at 09:00 UTC"`).
- **HONESTY:** copy never says "continuous monitoring" / "real-time" /
  "live" — banned by canvas §1.6. A pure-Go test
  (`TestHonestIntervalCopy`) + a vitest both assert the banned substrings are
  absent AND the interval is named. (This guard caught the author's first draft
  "this is not real-time monitoring" — honest in intent but containing the
  banned substring — which was rephrased to "recomputed on a schedule".)
- **Confidence:** medium on the exact numbers (tunable via
  `ATLAS_STALENESS_INTERVAL`); high on the honesty discipline.

### D5 — Idempotency via a dedicated `staleness_rollup_log` ledger (the one migration)

- **Options:** (a) no table — dedup by querying existing notifications;
  (b) a small claim-ledger table with a UNIQUE dedup key.
- **Chosen:** (b). The brief said "prefer reusing the existing store + a kind
  value (no migration) if possible; STOP and reconsider before adding a table."
  Reconsidered: deduping by scanning `notifications` would require encoding the
  dedup semantics (per-control-per-period alert; per-week digest) into a query
  over an opaque JSONB payload — fragile and slow. A dedicated claim ledger
  with `UNIQUE (tenant_id, recipient_user_id, dedup_key)` + `ON CONFLICT DO
NOTHING` makes a re-run a single-statement no-op (AC-5/AC-12). The table is
  additive, 4-policy FORCE RLS, reversible. This is the standard idempotency
  shape (mirrors `channel_delivery_log` / `email_delivery_log`).
- **Dedup key shape:** `alert:<control_id>:<band>:<recompute_period>` and
  `digest:<iso_week>`. Period = UTC truncation to the 6h boundary (epoch-
  relative — 00/06/12/18 UTC, NOT 09:00); ISO-week = `YYYY-Www`.
- **Confidence:** high.

### D6 — Recipients = ALL active users of the tenant

- **Options:** (a) only the bootstrap/admin user; (b) all active users;
  (c) a notification-target role.
- **Chosen:** (b). v1's primary user is the solo security leader (one active
  user), so "all active users" = "the operator" in the common case. Staleness
  is tenant-scoped operational posture every member legitimately sees; the
  per-user `in_app` opt-out (D7) is the individual escape hatch. A target-role
  surface is out of scope (P0-439-7).
- **Confidence:** medium (revisit if multi-user tenants want a target role).

### D7 — Opt-out = the EXISTING `userprefs` (`evidence_staleness` × `in_app`)

- **Options:** (a) add a new boolean opt-out column/table; (b) honor the
  existing per-kind preference matrix.
- **Chosen:** (b). `internal/auth/userprefs` already whitelists event
  `evidence_staleness` and channel `in_app` (slice 566/583). The producer reads
  that pref (default-on-missing-row, slice-108 D3) and suppresses delivery on
  an explicit `enabled=false`. AC-7 ("a minimum opt-out exists; default
  opted-in") is satisfied with ZERO new surface — no new prefs page
  (P0-439-7).
- **Confidence:** high.

### D8 — Digest body is CAPPED at top-10 + a link (threat-model D)

- The digest enumerates at most `DigestTopN = 10` stale controls (stale-first,
  then by control id), sets `truncated=true` beyond that, and always carries a
  freshness-view deep-link for the full list. Prevents an unbounded digest body
  for a tenant with a large stale corpus (P0-439-6).
- **Confidence:** high.

### D9 — Freshness-view deep-link target = `/dashboard#evidence-freshness`

- There is no dedicated `/freshness` route in `web/`; the freshness read model
  is surfaced by the dashboard's `EvidenceFreshnessPanel` (slice 040). AC-9's
  link points there via an anchor added to the panel's wrapping div. The Go
  `FreshnessViewPath` and the TS `FRESHNESS_VIEW_PATH` are kept identical.
- **Confidence:** high.

### D10 — Frontend surface = a node-testable presentation helper, not a component

- There is no in-app notifications-LIST component in `web/` yet (the slice-029
  store is exposed via the API client `listNotifications`, but no page renders
  it). Per the test-tier conventions (Q-3: vitest is the node-only
  module-logic tier; React component tests are out of scope for v1; Playwright
  is the de-facto component tier), AC-8/AC-9 are satisfied by a pure,
  node-testable presentation helper (`web/lib/api/staleness-notification.ts`)
  that maps the new kind to a label + the freshness link + honest copy, with a
  vitest. The component that consumes it is exercised by the e2e tier when an
  in-app notifications surface lands (a follow-on).
- **Confidence:** medium (the rendering is real + tested at module level; the
  visual surface is a follow-on, consistent with the thin-slice scope).

## Revisit once in use

1. **D2 — the 14-day approaching window.** Re-check noise vs. runway with real
   operators. Quarterly controls may find 14 days too tight; a very-active
   tenant may find it noisy. Candidate: make it per-class once per-control
   thresholds land (a known follow-on).
2. **D4 — the 6h recompute + Monday-09:00 digest.** Confirm the cadence feels
   right operationally; `ATLAS_STALENESS_INTERVAL` lets a deployment tune the
   recompute, but the weekly Monday-09:00 digest window is currently hard-coded
   — lift to config if operators want a different day/hour.
3. **D6 — all-active-users recipients.** If multi-user tenants emerge, decide
   whether staleness should target a specific role rather than everyone.
4. **D10 — the in-app rendering surface.** When the in-app notifications LIST
   component ships, wire the presentation helper into it and add the Playwright
   e2e assertion for the new kind + the freshness deep-link.
5. **Digest copy tone.** The copy is plain/factual by design; re-read it
   against the slice-182 tone anti-pattern list once a real digest is in front
   of a real operator.

## Spillover candidates surfaced (NOT fixed here)

- Per-class / per-control approaching-window + staleness thresholds (already a
  named follow-on in the slice's scope discipline).
- Configurable digest day/hour (currently Monday 09:00 UTC hard-coded).
- The in-app notifications LIST component + its Playwright e2e (the rendering
  surface this slice's presentation helper feeds).

These are captured as future follow-ons; none block this slice's ACs.
