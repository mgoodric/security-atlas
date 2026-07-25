# Slice 542 — decisions log (per-notification-kind email preferences)

**Parent:** slice 445 (email delivery substrate — master opt-in; D7 deferred
per-kind filtering). Depends on slice 108 (`user_notification_preferences` —
per-event `email` channel column, already shipped).

**Type:** JUDGMENT (filter composition semantics + default-on-vs-off per kind +
notification-type → slice-108-event mapping).

**detection_tier_actual:** none
**detection_tier_target:** none

(No bug surfaced during the build. The slice layers a pure-Go filter over the
already-shipped, already-tested slice-445 digest build and consults an
already-shipped slice-108 column. No migration. The filter semantics are
exercised by new pure-Go unit tests + two new integration tests; all passed on
first green after the wiring compiled.)

---

## Decisions

### D1 — Composition semantics: master AND per-kind (master is the outer gate)

**Decision.** The digest includes a notification kind iff
`(master email opt-in is ON) AND (that kind's per-event email pref is enabled)`.
The master switch (slice 445, default OFF) is the OUTER gate, evaluated FIRST in
`DeliverDigest`; the per-kind prefs are the INNER filter, evaluated only after
the master gate passes. Not master-OR-per-kind.

**Why.** master-AND is the only composition that cannot start delivering email
to a user who never opted in. master-OR would let a per-kind `email=true` row
(which slice 108 default-fills as `true` for every fresh user) override the
master OFF default — i.e. it would silently begin emailing every slice-108 user,
a regression of slice 445's "default opted-out" guarantee (P0-445-7 / P0-542-1).
The spec narrative names master-AND as the safe default; this implements it.

### D2 — Default-on-missing-row (absent per-kind row inherits the master opt-in)

**Decision.** When an opted-in user has NO `user_notification_preferences` row
for a kind's mapped event (the common case — fresh users have zero rows), the
kind is INCLUDED. A kind is suppressed only by an EXPLICIT `email=false` row for
its mapped event.

**Why (backward-compatibility — the load-bearing call).** This is the only
choice that does not silently start suppressing emails an opted-in user receives
today. Before this slice, an opted-in user received a digest covering ALL their
unread kinds. After this slice, that same user — who has set no per-kind prefs —
still receives every kind, because absence-of-row = default-on. The only
behavior change is for a user who has explicitly set a per-kind `email=false`
opt-out, which is exactly the new capability the slice exists to deliver. This
also inherits slice-108's own documented default-on-missing-row policy (slice
108 D3 / `userprefs.DefaultMatrix`), so the email digest and the
`/v1/me/preferences` matrix agree on what "no row" means.

The default is recorded as "master switch governs" — i.e. absence of a per-kind
row collapses the decision back to the master gate, which already passed. This
keeps the change additive: a per-kind row can only ever NARROW delivery for an
already-opted-in user, never widen it.

### D3 — Notification-type → slice-108-event mapping (NOT 1:1)

**Decision.** The notification `type` taxonomy and the slice-108 `event`
taxonomy are not 1:1. The mapping (`kindToEvent` in
`internal/notify/email/kindfilter.go`):

| notification `type`       | slice-108 `event`         | note                                |
| ------------------------- | ------------------------- | ----------------------------------- |
| `audit_period_assignment` | `audit_period_assignment` | identical                           |
| `policy_ack_due`          | `policy_ack_due`          | identical                           |
| `risk_review_overdue`     | `risk_review_overdue`     | identical                           |
| `control.drift`           | `control_drift`           | **dot → underscore** (load-bearing) |
| `audit_note.reply`        | _(no event)_              | UNMAPPED → included-when-master-on  |
| `evidence.staleness`      | _(no event)_              | UNMAPPED → included-when-master-on  |

**Why.** The slice-108 event whitelist (`internal/auth/userprefs.Events` + the
migration CHECK) is `{audit_period_assignment, policy_ack_due,
risk_review_overdue, control_drift}`. The slice-445 digest renders six
notification types (`internal/notify/email/message.go` `typeLabels`). Two of
them (`audit_note.reply`, `evidence.staleness`) have no slice-108 event row, so
there is no per-kind opt-out surface for them yet. UNMAPPED kinds default to
included-when-master-on (consistent with D2's "master governs when no pref
exists"). The `control.drift` (dot) vs `control_drift` (underscore) name
mismatch is the one non-obvious mapping and is pinned by a unit test
(`TestKindToEventMapping`) so a future taxonomy edit is deliberate, not
accidental.

**Forward note.** A future slice that adds a slice-108 event row for
`audit_note.reply` or `evidence.staleness` (giving those kinds a per-kind
opt-out surface) MUST also add the mapping entry here — mirroring slice 108's
"schema CHECK + whitelist move together" discipline. This is documented in the
`kindToEvent` doc comment.

### D4 — Filter location + shape: pure-Go map filter on the in-memory count map

**Decision.** The filter operates on the in-memory `counts` map inside
`DeliverDigest`, AFTER the RLS-scoped notification read and AFTER the master-gate
check, BEFORE the `unread == 0` skip check. The pure logic lives in a separate
`kindfilter.go` (no DB, no dbx import) so it is fast-unit-testable; the channel
bridges slice-108 rows to the pure filter via `emailChannelPrefMap`. The prefs
are read inside the SAME tenant-scoped read tx the digest already opens (one
extra query, no second pool/tx).

**Why.** Keeping the filter on the already-RLS-scoped count map means recipient
resolution and tenant scoping are structurally untouched (P0-542-2, threat-model
I): the filter can only remove a kind's count, never redirect delivery. The
pure/bridge split follows the slice-353 Q-2 pure-Go-pre-DB unit convention. The
total is recomputed after filtering so an all-muted digest collapses to
`unread == 0` and skips cleanly (no empty email sent).

### D5 — Minimum-disclosure body shape unchanged (P0-542-3)

**Decision.** No change to `BuildDigest` or the `DigestInput` shape. The filter
hands `BuildDigest` a narrowed `TypeCounts`/`TotalUnread`; the body template,
the closed `typeLabels` map, the deep-link, and the header-injection guards are
all untouched.

**Why.** The slice is a filter, not a body change. The minimum-disclosure
counts-only body (P0-445-4) and the header-safety guards (P0-445-2) are
slice-445 invariants this slice must not perturb (P0-542-3).

---

## P0 anti-criteria coverage

- **P0-542-1 (does NOT bypass the master opt-in).** The master gate is checked
  first in `DeliverDigest` (`if !optedIn { skip }`), unchanged from slice 445;
  the per-kind filter runs only after it passes. D1. Integration test
  `TestDeliverDigest_DefaultOptedOut` proves master-off → never send.
- **P0-542-2 (does NOT change recipient resolution or tenant scoping).** The
  filter operates only on the in-memory count map; the account-email lookup and
  tenant GUC path are untouched. D4. Integration test
  `TestDeliverDigest_PerKindMute_RemovesCountKeepsDelivery` asserts the same
  account email after a per-kind mute.
- **P0-542-3 (does NOT change the minimum-disclosure body shape).** `BuildDigest`
  - `DigestInput` unchanged. D5.

## Detection-tier classification

No defect surfaced during the build (`detection_tier_actual: none`,
`detection_tier_target: none`). The filter semantics are covered at the unit
tier (`kindfilter_test.go`, fast pure-Go table tests) and the
filter-meets-RLS-digest behavior at the integration tier
(`integration_test.go`, real Postgres + RLS).
