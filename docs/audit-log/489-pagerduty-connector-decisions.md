# 489 — PagerDuty connector (incident-response evidence): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind shapes, the on-call-identity-vs-PII boundary, the incident
look-back window, the scope minimums, the `x-default-scf-anchors`, and the
stable-field choices). It does NOT block merge — the maintainer iterates
post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No product-behavior bug surfaced during the build. One test-fixture bug was
caught at the green step: the `pdrecord` idempotency test seeded `observed_at`
at 12:34 and added 30 minutes, crossing the hour boundary and breaking the
hour-truncation dedup assertion; the fixture was moved to 12:00. That is a test
authoring error, not a defect in shipped behavior.)

## Decisions made

### D1 — Two split evidence kinds, NOT one shared kind, and NOT shared with slice 488

- **Options considered:** (a) two PagerDuty-specific kinds
  (`pagerduty.oncall_coverage.v1` + `pagerduty.incident_summary.v1`); (b) one
  combined `pagerduty.incident_response.v1` kind; (c) reuse / extend slice 488's
  `monitoring.alert_config.v1`.
- **Chosen:** (a), two split kinds, self-contained under `connectors/pagerduty/`.
- **Rationale:** on-call coverage and incident summaries answer different control
  questions (coverage/staffing = IRO-04/IRO-07/CC7.4; incident detect-handle-
  resolve = IRO-02/IRO-09/CC7.3-7.5) and have disjoint field sets — a combined
  kind would be a union schema with two mutually-exclusive halves (lossy/awkward
  for the evaluator). Reusing slice 488's `monitoring.alert_config.v1` fails the
  lossy test: an escalation policy is not an alert rule and an incident is not a
  monitor; forcing them through the alert-config tuple would drop the tier/
  on-call/incident-lifecycle structure. PagerDuty is its own IR domain, so —
  unlike the Datadog/Grafana pair which share an identical alert-config altitude
  — there is no genuine shared shape to factor out. The record builder lives
  once in `connectors/pagerduty/internal/pdrecord`.

### D2 — On-call-identity-vs-PII boundary (THE load-bearing guard, threat-model I / P0-489-3)

- **Decision:** the on-call **identity** needed to prove coverage is in scope
  (the on-call user's / schedule's opaque PagerDuty id + display name); a
  responder's **personal contact detail** (phone number, personal email) is NOT.
- **Enforcement:** structural, at the decode boundary. The escalation-policy
  client decodes only `escalation_policies[].id/name`,
  `escalation_rules[].targets[].id/type/summary`. PagerDuty exposes a user's
  phone/email on the separate `/users` resource and on the user object's
  `contact_methods` — the connector NEVER fetches `/users` and NEVER decodes a
  contact field, so the PII cannot enter memory. The `oncall.RawTarget` /
  `oncall.OnCall` Go types have no phone/email field by construction, making a
  leak a compile error rather than a runtime guard. A test
  (`integration_test.go:TestEmittedRecords_NoPIIorFreeText`) asserts no
  PII-shaped substring (`@`, `+1555`, `phone`, …) reaches an emitted payload.
- **Rationale:** display name is the minimum that makes the evidence meaningful
  to an auditor ("who is on call?"); personal phone/email add no audit value and
  are the highest-PII-cost field PagerDuty holds.

### D3 — Incident SUMMARY only: no free-text title/body/notes/postmortem (threat-model I / P0-489-3)

- **Decision:** the incident record carries id / number / urgency / status /
  service id+name / created+resolved timestamps. It does NOT carry the incident
  `title`, `description`/body, notes, or postmortem free-text.
- **Note on `title`:** PagerDuty's incident `title` is a short summary line and
  is the field an auditor might expect. It is deliberately EXCLUDED: a title is
  operator-authored free-text that routinely embeds customer names, affected-
  account identifiers, and "suspected breach" phrasing (the fixture
  `incidentsJSON` deliberately includes a customer-PII title to prove it is
  dropped). The slice spec lists "id / urgency / status / created+resolved
  timestamps / service" and pointedly omits title; staying strictly to that list
  keeps the connector on the right side of the over-collection guard. If an
  operator later needs a title, that is a deliberate follow-on with its own
  redaction story, not a v0 default.
- **Enforcement:** the incidents client decodes only the summary fields; the
  `incidents.RawIncident` type has no Title/Body/Notes field.

### D4 — Incident look-back window default = 90 days, configurable, bounded (threat-model D)

- **Decision:** `--lookback-days` defaults to 90; the run queries
  `since = now - lookback-days`, `until = now`, with a bounded page size (100)
  and a single page in v0. `lookback-days <= 0` is rejected at PreRunE.
- **Rationale:** 90 days matches a common SOC 2 observation increment while
  keeping each run bounded; the bound is honest (cursor pagination across the
  full window is a documented follow-on, not v0).

### D5 — `x-default-scf-anchors` (maintainer recheck — OQ #9)

- `pagerduty.oncall_coverage.v1` → `["IRO-04", "IRO-07"]` (Incident Response
  Plan; Incident Response Team). Maps to SOC 2 CC7.4.
- `pagerduty.incident_summary.v1` → `["IRO-02", "IRO-09"]` (Incident Handling;
  Incident Reporting). Maps to SOC 2 CC7.3 / CC7.4 / CC7.5.
- These are DEFAULT mapping hints, flagged for maintainer accuracy recheck per
  OQ #9 (load-bearing). They are not asserted against the SCF catalog by any
  drift test (anchors are advisory; the drift guard only requires kind-set
  agreement between the schema files and `DefaultSeed`).

### D6 — Stable optional fields + idempotency keys (feedback_connector_patterns)

- **Stable `actor_id`:** `connector:pagerduty:oncall@<version>` /
  `connector:pagerduty:incidents@<version>` — distinct service segments per
  evidence surface, per the cross-connector convention.
- **`observed_at` granularity:** hour-truncated (UTC), matching the slice
  004/486/487/488 dedup contract.
- **Idempotency keys:** `sha256("pagerduty.oncall_coverage|<policy_id>|<hour>")`
  and `sha256("pagerduty.incident_summary|<incident_id>|<hour>")` — same-entity
  re-runs within the hour collapse to one ledger row.
- **Stable optional fields:** `tiers` / `on_call` omitted-when-empty on coverage;
  `incident_number` / `service_id` / `service_name` / `resolved_at`
  omitted-when-zero on incidents (an open incident has no `resolved_at`).

### D7 — Scope minimums

- Every record carries `service` (default `pagerduty`) + required
  `--environment`. `Result = INCONCLUSIVE` (descriptive; the platform evaluator
  owns pass/fail per (control, scope)).

### D8 — Shared thin HTTP transport (`pdhttp`) instead of per-domain duplication

- Both collectors need the identical read-only `Authorization: Token token=…`
  header + `Accept: …pagerduty+json;version=2` + bounded GET-JSON path. The
  transport lives once in `connectors/pagerduty/internal/pdhttp`; the `oncall`
  and `incidents` clients wrap it. This is the simplify-altitude call: one
  transport, two domain clients (mirrors the spirit of slice 488's shared
  helpers, scoped to this one connector).

## Revisit once in use

- **R1.** SCF anchor accuracy (D5) — maintainer confirms IRO-04/07 and IRO-02/09
  are the right anchors for the two surfaces (OQ #9).
- **R2.** Incident `title` exclusion (D3) — if auditors ask for a human-readable
  incident label, design a redacted/operator-curated label field rather than
  passing raw title through.
- **R3.** Look-back default (D4) — confirm 90d fits the typical audit window;
  add full-window cursor pagination if accounts exceed one bounded page.
- **R4.** Pagination (v0 reads the first bounded page only) — promote to cursor
  pagination when a real account exceeds the page on either surface.
- **R5.** On-call resolution depth — v0 reports the escalation-policy targets
  (user/schedule references). If "who is on call RIGHT NOW behind a schedule"
  is needed, add a read of `/oncalls` (still identity-only, never contact
  detail).

## Spillover slices filed (out of v0 scope)

- **538** — postmortem / retrospective evidence (with a redaction story).
- **539** — responder-performance metrics.
- **540** — event-driven profile via PagerDuty incident-lifecycle webhooks.
