# OE-661 — personnel-security runtime wiring (HRIS trigger + overdue sweep) — decisions log

**Type:** JUDGMENT
**Source:** Plane OPENENGINE-661 (parent OE-630, PR #1547). OE-630 landed
`internal/personnelsecurity` as a library — `HandleWorkerEvent` and
`SurfaceOverdueOffboarding` had no callers outside tests. This child wires
both into the runtime: HRIS joiner/leaver events through the connector path
create checklists automatically, and a scheduled sweep surfaces overdue
offboarding checklists as high-priority notifications.

The build-time subjective calls are recorded here per the JUDGMENT-slice
process. None touch the runtime AI-assist boundary (no LLM involved), and the
parent's hard boundary holds throughout: evidence and task tracking only —
this wiring never provisions or deprovisions access.

---

## D1 — consumption point: durable JetStream consumer on the evidence-ingest stream

**Decision:** Worker-event consumption lives platform-side as a new durable
consumer (`personnel_security_worker`) on slice 015's EVIDENCE_INGEST
JetStream stream (`internal/personnelsecurity/subscriber.go`), NOT as a hook
inside the connectors and NOT as an inline call in the ingest service.

**Rationale:**

- Constitutional invariant #3: every worker-lifecycle fact already leaves the
  Rippling/BambooHR connectors through the one canonical wire —
  `EvidenceIngestService.Push` — and lands on the stream. That stream IS the
  platform-side point where worker status transitions become observable; no
  new hook surface is needed.
- Independent durable consumers on a Limits-retention stream each receive
  every message (the slice 012 eval-subscriber / slice 016 freshness-drift
  shape), so the checklist reaction never blocks or races the append-only
  ledger write (invariant #2 — evaluation-side reactions never sit inside the
  ingestion stage).
- A connector-side hook would put platform write logic in a source-side
  process holding source credentials — the wrong side of the SDK contract.

**detection_tier_actual:** `none` · **detection_tier_target:** `integration`

## D2 — control resolution: connector-bound control UUID pass-through, else nil + `DefaultControlRef`

**Decision:** The subscriber passes through the control the connector
operator bound the roster push to when `control_id` parses as a UUID;
otherwise (SCF-anchor ref such as `scf:IAC-07`, or empty) it falls back to
`uuid.Nil`, and the checklist's completion evidence carries the library's
`DefaultControlRef` (`soc2:cc1/access-controls`) — exactly like a manual
checklist created without a control. NOT a new tenant setting, NOT a
well-known-control DB lookup at consume time.

**Rationale:**

- The connector credential already carries an operator-chosen control binding
  for the roster evidence; reusing it means the checklist and the roster
  evidence point at the same control with zero new configuration surface.
- A hardcoded well-known SCF/CC1 lookup would guess wrong for tenants whose
  catalog import differs, and a per-tenant setting is configuration nobody
  asked for yet (v1 persona is the solo operator). `DefaultControlRef` was
  built by OE-630 for precisely this fallback; the SOC 2 CC1 anchor is
  preserved at evidence time either way.
- Revisit trigger: if a tenant needs per-workflow-kind control routing, that
  is a small follow-up slice adding a tenant setting — the seam is the one
  `uuid.Parse` site in `subscriber.go`.

**detection_tier_actual:** `none` · **detection_tier_target:** `unit`
(subscriber_test.go covers both the UUID pass-through and the anchor-ref
fallback branches)

## D3 — stable source event id: derived from the lifecycle fact, not the push idempotency key

**Decision:** The source event id passed to `HandleWorkerEvent` is derived
from the fact itself — `<worker_id>|<workflow_kind>|<event-date>` (start date
for a joiner, end date for a leaver, YYYY-MM-DD, empty when the source
reported none) — NOT the connector's push idempotency key. A 30-day recency
window (`EventRecencyWindow`, relative to the record's observed-at) gates
checklist creation.

**Rationale:**

- The connector's push idempotency key is hour-truncated: a roster
  re-observation within the hour collapses at the ledger, but a fresh hour
  re-observes the same lifecycle fact as a NEW record. Keying the checklist
  on that would create one checklist per hour per worker. The fact-derived id
  makes every re-observation of the same fact map to the same
  (tenant, source, source_event_id), so the store's dedup plus the DB unique
  index yield exactly one checklist; a rehire (new start date) or a later
  termination (new end date) is genuinely new and gets a fresh one.
- The recency window exists because the pull profile re-observes the FULL
  roster: without it, a tenant's first connector sync would backfill a
  checklist — and, for leavers, an instantly-overdue high-priority
  notification — for every worker hired or terminated years ago. Thirty days
  matches the honest meaning of "event": recent enough that the
  onboarding/offboarding work is plausibly still actionable. An undated fact
  is exempt (the first observation is the platform's first knowledge of it).

**detection_tier_actual:** `none` · **detection_tier_target:** `integration`
(the replay half of `TestWorkerEventThroughIngestionPathCreatesChecklistOnce`
re-pushes the same fact an hour later — fresh push key, same derived id — and
asserts one checklist)

## D4 — recipient resolution: the tenant's active users

**Decision:** The overdue sweep notifies each of the tenant's ACTIVE users
(`ListActiveUsersForTenant`, the staleness-rollup convention), enumerated
under the tenant's own GUC through the app-role store. NOT a per-tenant
security-owner setting (none exists), NOT the checklist creator (connector
checklists have `created_by = NULL`).

**Rationale:** The issue's fallback guidance applied: there is no per-tenant
security-owner convention in the notify scheduler (its recipients are opt-in
digest subscribers, wrong shape for an alert), so the sweep uses the
established active-users convention. The v1 persona is the solo security
leader, so "the tenant's active users" IS the security-owner set today.
Idempotency makes the breadth safe: the notification row itself is the dedup
marker — one `personnel_security.offboarding_overdue` notification per
(checklist, recipient), ever — probed and inserted in the same transaction
(`CountPersonnelOverdueNotificationsForChecklist`), so re-runs never
double-notify.

**detection_tier_actual:** `none` · **detection_tier_target:** `integration`
(`TestOverdueSweepNotifiesOnceAndIsolatesTenants` asserts once-per-recipient,
no growth on re-run, and cross-tenant silence)

## D5 — sweep shape and cadence: notify-scheduler pattern, daily

**Decision:** `OverdueNotifier` (`internal/personnelsecurity/overdue.go`)
follows the shared periodic-sweep shape of `internal/notify/scheduler` and
the slice-055 decision-overdue notifier: in-process tick loop (single-VM
self-host target, no external cron), immediate inline first sweep, a
migrator-role (BYPASSRLS) query that enumerates ONLY the tenant ids with
overdue open offboarding checklists
(`ListTenantsWithOverdueOffboardingChecklists` — ids, never checklist
content), then per-tenant work under that tenant's GUC with
try/log/continue so one failing tenant never aborts the rest. Cadence is
daily (`DefaultOverdueSweepInterval`; `ATLAS_PERSONNEL_OVERDUE_INTERVAL`
overrides for dev loops).

**Rationale:** A checklist becomes due 24h after the termination date, so a
daily sweep surfaces it at most a day late — acceptable for an in-app
notification, and honest-interval discipline (canvas anti-pattern) means we
name that cadence rather than calling it continuous monitoring. The
cross-tenant enumeration mirrors `ListTenantsWithOverdueDecisions` so RLS
review stays one pattern.

**detection_tier_actual:** `none` · **detection_tier_target:** `integration`
