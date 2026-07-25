# Slice 566 — decisions log (per-kind email opt-out for the unmapped notification kinds)

**Parent:** slice 542 (per-notification-kind email filter — D3 flagged
`audit_note.reply` + `evidence.staleness` as UNMAPPED and left the forward note
that a slice adding their event rows MUST also add the mapping). Depends on slice
108 (`user_notification_preferences` — the event taxonomy + UI matrix this
extends).

**Type:** JUDGMENT (slice-108 event-taxonomy extension + UI toggle addition).

**detection_tier_actual:** none
**detection_tier_target:** none

(No bug surfaced during the build. The slice widens an existing CHECK constraint,
appends two entries to an existing Go whitelist + an existing `kindToEvent` map,
and adds two UI rows mirroring four existing ones. The CHECK widening was
verified against a freshly-migrated Postgres — accepts the two new values,
rejects garbage; the filter behavior is covered by extended pure-Go unit tests +
the existing userprefs/notify integration suites, all green on first run.)

---

## Decisions

### D1 — Event-key spelling: `audit_note_reply` + `evidence_staleness` (dot → underscore)

**Decision.** The two new slice-108 event keys are `audit_note_reply` and
`evidence_staleness` (underscore form), mapped from the notification `type`
strings `audit_note.reply` and `evidence.staleness` (dot form) via the
`kindToEvent` map — the SAME dot→underscore normalization slice 542 already
applies for `control.drift` → `control_drift`.

**Why.** The slice-108 event taxonomy is uniformly underscore-cased
(`audit_period_assignment`, `policy_ack_due`, `risk_review_overdue`,
`control_drift`); the notification-`type` taxonomy mixes dotted
(`audit_note.reply`, `evidence.staleness`, `control.drift`) and underscored
forms. Keeping the new events underscore-cased is consistent with the existing
event whitelist (the column is queried by event key in
`/v1/me/preferences` and the settings UI), and the dot→underscore translation
already has a home (the `kindToEvent` map, pinned by `TestKindToEventMapping`).
Keeping the dotted form as the event key would have made `evidence.staleness` the
only dotted entry in `userprefs.Events` — gratuitous inconsistency. The
notification `type` strings themselves are unchanged (they remain dotted in
`internal/audit/notifications/dispatch.go` + `internal/notify/email/message.go`);
only the slice-108 _event_ spelling is chosen here.

### D2 — `evidence.staleness` gets its OWN event (not folded into an existing one)

**Decision.** `evidence.staleness` becomes a first-class slice-108 event
(`evidence_staleness`), not folded into (e.g.) `control_drift` or an
"evidence" umbrella event.

**Why.** A per-kind opt-out is only meaningful at the granularity the user
reasons about. Stale-evidence digests and control-drift alerts are distinct
operational signals with distinct noise profiles — a user who wants drift alerts
but not staleness reminders must be able to express exactly that. Folding would
re-create the very coarseness this slice exists to remove. The one-event-per-kind
shape also keeps the `kindToEvent` map a clean 1:1 for the now-mapped kinds and
matches the existing pattern (every other rendered digest kind has its own
event). Same reasoning applies to `audit_note_reply`.

### D3 — Whitelist-move-together: CHECK constraint + Go whitelist in the same PR

**Decision.** The two new event values land atomically in BOTH the migration's
`user_notification_preferences_event_check` CHECK constraint (new migration
`20260607040000_userprefs_unmapped_kinds.sql`) AND `internal/auth/userprefs.Events`.

**Why.** This is the slice-108 schema-and-whitelist-move-together discipline
(documented in the `userprefs` package doc + the slice-108 migration comment +
slice 542's `kindToEvent` doc). A value in `userprefs.Events` but absent from the
CHECK is a latent 500: the handler accepts the event, the UPSERT then violates
the CHECK at write time. The reverse (CHECK admits a value the Go whitelist
rejects) strands the surface — the UI could not even offer the toggle. Both
must move together; this PR moves both.

### D4 — Migration is a monotonic CHECK widening, no data writes (backward-compat)

**Decision.** The forward migration ONLY drops + re-adds the CHECK with the two
extra admitted values. It inserts NO `user_notification_preferences` rows.

**Why (P0-566-1, the load-bearing call).** Default-on-missing-row (542 D2 / 108
D3) must be preserved: an opted-in user with no row for the new events keeps
receiving both kinds until they set an explicit per-kind `email=false`. Inserting
rows would either (a) pre-seed `email=true` (a no-op given default-on, but
needless write churn across every user) or (b) pre-seed `email=false` (a SILENT
SUPPRESSION regression — exactly the anti-criterion). Writing nothing is the only
choice that changes behavior solely for users who explicitly opt out post-deploy.
The CHECK widening is monotonic (it only enlarges the admitted set), so it is
safe to apply over existing data — no existing row can violate the wider
predicate. The `.down.sql` tightens the CHECK and first DELETEs any rows for the
two new events (a tighter CHECK would otherwise reject existing data on
re-add); deleting user preference DATA on a down-migration is acceptable only
because a down-migration is an operator-initiated rollback, and the comment says
so.

### D5 — UI rows mirror the four existing per-event rows exactly

**Decision.** The settings notification matrix gains two rows
(`audit_note_reply`, `evidence_staleness`) with the same `{key,label,description}`
shape, the same in-app + email toggle pair, and the same `data-testid`
convention (`settings-notif-row-<key>`, `settings-notif-<key>-{in-app,email}`)
as the four existing rows. The Playwright settings spec's row-iteration loop is
extended to assert both new rows render.

**Why.** The matrix already iterates `NOTIF_EVENTS`; adding two entries surfaces
the toggles with zero new component code. Mirroring the testid convention means
the existing e2e visibility assertion covers the new rows by simply extending the
key list. Copy: "Audit-note replies" / "Replies on audit-note threads you're
part of" and "Stale evidence" / "Digest of evidence whose freshness window has
lapsed" — descriptive, matching the tone of the four existing descriptions.

---

## P0 anti-criteria coverage

- **P0-566-1 (does NOT change slice-542 default-on-missing-row).** The migration
  writes no rows (D4); `emailEnabledForKind` still returns `true` for a mapped
  event with no row (default-on, unchanged). Unit test
  `"slice-566 kind (...) no row -> default-on (send)"` proves an opted-in user
  with no per-kind row still receives both new kinds; no silent suppression.
- **P0-566-2 (does NOT change recipient resolution / tenant scoping / body shape).**
  This slice touches only the event whitelist (Go + CHECK), the `kindToEvent`
  map, and the settings UI. `DeliverDigest`, `BuildDigest`, recipient lookup, and
  the tenant GUC path are untouched (inherits P0-542-2 / P0-542-3).
- **P0-566-3 (CHECK constraint + `userprefs.Events` land in the same slice).** D3.
  Both whitelists carry exactly `{audit_period_assignment, policy_ack_due,
risk_review_overdue, control_drift, audit_note_reply, evidence_staleness}` —
  verified: the migrated DB's `pg_get_constraintdef` lists all six and
  `userprefs.Events` lists the same six.

## Detection-tier classification

No defect surfaced during the build (`detection_tier_actual: none`,
`detection_tier_target: none`). The mapping + filter semantics are covered at the
unit tier (`kindfilter_test.go`: the two new kinds now map, are mutable via an
explicit opt-out, and remain default-on with no row); the CHECK-meets-whitelist
behavior at the integration tier (the existing `internal/auth/userprefs` +
`internal/api/me` integration suites run against a Postgres carrying the new
migration); and the UI rows at the e2e tier (the extended settings Playwright
spec).
