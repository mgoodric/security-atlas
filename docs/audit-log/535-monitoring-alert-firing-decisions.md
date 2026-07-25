# Slice 535 — monitoring alert-firing-history (Datadog + Grafana) — decisions log

- detection_tier_actual: none
- detection_tier_target: none

Type: JUDGMENT. This log records the subjective build-time calls. It does not
block merge; the maintainer iterates post-deployment from the "Revisit once in
use" list.

## Decisions made

### D1 — Profile shape: bounded PULL, honestly named (THE load-bearing call)

**Options considered.** (a) Bounded pull of the last-N-hours of firing events on
the operator's scheduled cadence; (b) a genuine event-driven `subscribe` profile
backed by a real vendor push/webhook the connector receives.

**Chosen: (a) bounded pull**, mirroring the slice-636 `datadog.siem_signal.v1`
precedent. `profiles_supported` registers `pull`; both `cmd_run.go` docstrings
and both READMEs state explicitly "NOT continuous monitoring and NOT
event-driven."

**Rationale.**

1. **Honesty (P0).** Neither vendor source is a push surface the connector
   _receives_. Datadog's Events API (`GET /api/v1/events?sources=monitor_alert`)
   is a search/poll surface; Grafana's alert state-history (`GET /api/v1/rules/
history`) is a query surface. Alert notifications route to the operator's _own_
   Slack/PagerDuty/webhook integrations, never back to this connector. Labeling a
   24h poll "event-driven" or "continuous monitoring" would be the exact
   anti-pattern the slice's P0 bans.
2. **No novel inbound receiver.** A `subscribe` profile would require standing up
   an inbound webhook receiver — which would also collide with invariant #3
   (the platform-side wire is always push; a connector is not an inbound surface).
   Bounded pull keeps the connector a pure outbound `Push` peer.
3. **Precedent.** Slice 636 made the identical call for SIEM signal history; this
   is the established shape for "stream of timestamped events from a poll API."

A `--<vendor>-firing-lookback` flag (default 24h) names the window honestly on
both connectors.

**Confidence: high.**

### D2 — Vendor-neutral shared kind vs per-vendor kinds

**Chosen:** one vendor-neutral `monitoring.alert_firing.v1` emitted by both
connectors, with a shared `connectors/monitoring/firing` normalization layer +
`connectors/monitoring/firingrecord` builder — exactly mirroring how slice 488's
`monitoring.alert_config.v1` is shared via `alertcfg` + `monrecord`.

**Rationale.** A firing event reduces to the same shape across both vendors
(rule_id + state + fired_at + resolved_at + routing-target handle); there is no
vendor-specific field that forces a sibling split (unlike `datadog.siem_rule.v1`,
which carries a Datadog-only detection-class field — the slice-488 D1 split). The
firing surface is the config surface's sibling, so it reuses the config surface's
sharing pattern.

**Confidence: high.**

### D3 — SCF anchors: MON-01 + IRO-09 (IRO-02 substituted)

**Spec candidate:** MON-01 + IRO-02.

**Chosen:** **MON-01 + IRO-09.**

**Rationale.** I verified each candidate against the bundled SCF catalog fixture
(`migrations/fixtures/scf-sample.json`) before using it:

- `MON-01` — **present** ✓ (used by `monitoring.alert_config.v1`).
- `IRO-02` — **ABSENT** ✗. `grep -c '"IRO-02"' migrations/fixtures/scf-sample.json`
  returns 0. (Note: the `pagerduty.incident_summary` schema _file_ lists IRO-02 in
  its `x-default-scf-anchors`, but that anchor is not in the fixture either — the
  slice-068 drift guard checks _control-bundle_ anchors against the fixture, not
  schema `x-default-scf-anchors`, so that dangling schema anchor goes uncaught.
  I did not want to add a second dangling anchor.)
- `IRO-09` — **present** ✓ (Incident Reporting; used by `datadog.siem_signal.v1`
  and `pagerduty.*`).

IRO-09 (Incident Reporting) is the closest present incident anchor to IRO-02
(Incident Handling): a firing event is the raw signal that an incident-handling
process has something to report on. The anchors are documented as default mapping
hints flagged for maintainer recheck in the schema description.

**Confidence: medium** (the IRO-09-vs-a-future-IRO-02 mapping is a maintainer
recheck once the SCF catalog gains IRO-02).

### D4 — Idempotency / dedup key: (vendor, rule_id, fired_at) + observed hour

**Chosen:** `idem.AlertFiringKey` =
`sha256("monitoring.alert_firing|<vendor>/<rule_id>/<fired_at_RFC3339>|<hour>")`.

**Rationale.** Unlike the config kind (one config per rule → key on
`(vendor, rule_id)`), a busy rule fires many distinct times and each firing is its
own audit-relevant event, so `fired_at` is part of the key. Including the firing
instant is what makes an **overlapping look-back window re-read** collapse onto
the same ledger row instead of double-writing (threat-model R). The observed UTC
hour is folded in to match the config key's hour-granularity replay shape;
`fired_at` is rendered to the second so two firings within the same hour stay
distinct rows.

**Confidence: high.**

### D5 — Firing-events-only structural over-collection guard (Information Disclosure DOMINANT)

**Chosen:** the guard is **structural**, at the shared `firing.RawFiring` /
`firing.Firing` / `firing.Target` layer. Those structs have no field capable of
holding an alert **message body**, the triggering **metric values**, the secret
**webhook URL**, or recipient PII. The vendor clients decode only the body-free
fields (Datadog: alert_type + monitor_id + date_happened + the parsed `@handle`;
Grafana: the line's `current` state + `ruleUID` + the contact-point label) — the
event text, the `values`/metric map, the webhook secret, and recipient-email
labels/tags have no struct field, so `json.Decode` discards them.

**How it is enforced:**

1. `TestStructuralOverCollectionGuard` in `connectors/monitoring/firing` pins the
   field set by reflection and fails the build if a field name matches a banned
   substring (message/body/text/metric/value/secret/url/webhook/email/...).
2. The Datadog drop test (`firingevents/client_test.go`) feeds an Events payload
   carrying `test-message-body-should-be-dropped`, metric values, a
   `secret-webhook-should-be-dropped` URL, and `bob@corp.test`, and asserts none
   reaches a record.
3. The Grafana drop test (`alerthistory/client_test.go`) feeds a state-history
   line carrying an annotation body, a `values` map, a webhook secret, and a
   `user_email` label, and asserts none reaches a record.
4. An email-shaped routing handle (`@user@corp.test`) is dropped by
   `firing.sanitizeTarget` — recipient PII never becomes a target.

Neutral test markers only (no vendor-shaped token/URL literals) so GitGuardian's
branch-scoped scan does not flag fixtures.

**Confidence: high.**

### D6 — Datadog source: Events API monitor_alert events; read-only `events_read`

**Chosen:** `GET /api/v1/events?sources=monitor_alert` with the new read-only
`events_read` Application-key scope (added to `RequiredScopes()` + `permissions`).

**Rationale.** The monitor-alert event stream is the canonical record of monitor
`overall_state` transitions (alert/recovery). `events_read` is the least-privilege
read minimum; the connector issues only GETs and never a write verb (`events_write`
is explicitly banned in the README + `permissions` output).

**Confidence: medium** (the Events-API `alert_type` → firing-state mapping is the
spot most likely to want a real-data recheck — see Revisit).

### D7 — DoS bounding (threat-model D, load-bearing)

**Chosen:** bounded look-back window (per-vendor flag, default 24h) + a per-run
cap (Datadog `maxPages=50`×`pageLimit=100` ⇒ 5,000 events; Grafana
`maxTransitions=5000`) + a 60s run timeout on each client. A busy/flapping alert
can fire thousands of times; on cap-exceed the run stops with
`ErrEventCapExceeded` / `ErrTransitionCapExceeded` and reports honestly rather
than reading unbounded.

**Confidence: high.**

## Revisit once in use

- **D6 / Datadog `alert_type` mapping** — re-check the Events-API `alert_type`
  → firing-state fold (`error`/`warning` ⇒ alerting, `success` ⇒ resolved)
  against a real Datadog org's monitor-event stream; some monitor types emit
  `info`/`user_update` events that this v0 folds to `alerting` by the conservative
  default. Confirm a `warn` monitor transition is desired as `pending`.
- **D3 / anchors** — re-map `monitoring.alert_firing.v1` to IRO-02 (Incident
  Handling) if/when the SCF catalog fixture gains IRO-02; until then IRO-09 is the
  substitute. Also fix the pre-existing dangling IRO-02 in the
  `pagerduty.incident_summary` schema if the fixture stays without it.
- **D1 / profile** — revisit ONLY if a vendor ships a real state-history webhook
  the connector can receive (then a genuine `subscribe` profile becomes honest);
  today both sources are query surfaces, so bounded pull is the correct + honest
  shape.
- **Grafana state-history shape** — the `GET /api/v1/rules/history` Loki-frame
  decode (`data.values[0]` = timestamps, `data.values[1]` = JSON lines) is coded
  to the documented frame shape; verify against a live Grafana 10/11 deployment,
  as the frame envelope has historically shifted between Grafana minor versions.
- **resolved_at derivation** — v0 emits one record per state transition with the
  transition's own `state`; it does not pair an `alerting`→`resolved` transition
  into a single record with both `fired_at` and `resolved_at` (the evaluator can
  do that pairing from the ledger). Revisit if auditors want a pre-paired
  firing/resolution record.

## Confidence summary

| Decision                                        | Confidence |
| ----------------------------------------------- | ---------- |
| D1 profile = bounded pull                       | high       |
| D2 vendor-neutral shared kind                   | high       |
| D3 anchors MON-01 + IRO-09 (IRO-02 substituted) | medium     |
| D4 dedup key (vendor, rule_id, fired_at)        | high       |
| D5 structural over-collection guard             | high       |
| D6 Datadog Events API + events_read             | medium     |
| D7 DoS bounding                                 | high       |
