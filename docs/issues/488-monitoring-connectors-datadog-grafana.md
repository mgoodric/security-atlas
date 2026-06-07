# 488 — Monitoring connectors (Datadog + Grafana) — logging/alerting config evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors (`aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual`; canvas §10.1, `connectors/`); the
planned layout (`CLAUDE.md`, "Planned repository layout") names `datadog` and
`grafana` in the observability/monitoring tier. For the platform's persona — a
SaaS startup security leader — **monitoring/alerting configuration is a recurring
SOC 2 CC7.2 ("the entity monitors system components") and CC7.3/CC7.4 evidence
demand**: "prove you have monitors/alerts configured and notification routing to
on-call." Today that is a manual screenshot upload.

This slice clusters **two closely-related monitoring connectors** — Datadog and
Grafana — because they answer the same control question (monitoring/alerting is
configured) and share an identical evidence shape, so one slice keeps each a
tracer-bullet while proving the monitoring evidence-kind family. Both follow the
slice-004 / 442 connector template (stable `actor_id`, stable optional fields,
`observed_at` granularity, register-per-run, scope minimums, vendor-native auth;
`feedback_connector_patterns`).

- **`connectors/datadog/`** collects **monitor/alert inventory** (each monitor's
  name, type, enabled state, notification targets — NOT recipient PII bodies)
  via the read-only Datadog API.
- **`connectors/grafana/`** collects **alert-rule + notification-policy
  inventory** (alert rules, their enabled state, contact-point routing — names,
  not secrets) via the read-only Grafana API.

Both register `profiles_supported` per run and `Push` each record to the single
`IngestEvidence` API.

**Scope discipline.** **Two connectors, one evidence surface each** (alert/monitor
configuration inventory), the minimum that demonstrates the monitoring evidence
family is a real first-class peer set. Neither ships **dashboard JSON / metric
time-series / log query results** (deliberately — config inventory only, not
telemetry payloads), neither ships a webhook/event-driven profile (pull-profile
only in v0 — honest interval), and neither changes the platform-side wire
(push-only — invariant #3). **Follow-on slices:** Datadog SIEM/Cloud-SIEM rule
evidence; Grafana SAML/RBAC config evidence; alert-firing-history (event-driven)
profiles.

## Threat model (STRIDE) — connector family (source-credential heavy)

Each connector is a separate process holding **source-side credentials** (a
Datadog API+APP key pair / a Grafana service-account token). The dominant risks
are credential handling (over-broad keys, key leakage), over-collection (contact
points and notification configs can embed webhook URLs / tokens / PII), and
keeping the platform wire push-only.

**S — Spoofing.** Each connector authenticates TO the platform via its push
credential (the existing connector auth — OAuth client_credentials per slice 191) and TO its source via the vendor key/token. Risk: a stolen push credential,
or a source key with write/admin scope.
**Mitigation:** push reuses the existing connector credential boundary; Datadog
auth uses a key with **read-only** scope (e.g. `monitors_read`), Grafana auth
uses a **Viewer**-role service-account token — documented as the required
minimum. Source credentials stay source-side; the platform never sees them
(invariant #3).

**T — Tampering.** Evidence records carry a sha256 content-hash.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); ingest validates the hash. The connectors do not accept inbound
data — they only read the source + push.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
record carries a stable `actor_id` (the datadog/grafana connector + run context)
and `observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure.** Notification configs can embed webhook URLs,
integration tokens, and recipient email addresses (PII). Risk: the connector
copies a secret-bearing webhook URL or recipient PII into an evidence record, or
logs the source key.
**Mitigation:** the connectors collect **monitor/alert + routing-target
metadata** — monitor/rule name, type, enabled state, and the _handle/channel
name_ of a notification target — NOT the secret webhook URL, NOT integration
tokens, NOT recipient email addresses. A test asserts no secret URL / token /
recipient PII enters an evidence record. Source keys are never logged.

**D — Denial of service.** A large org (thousands of monitors/rules) could make
a run unbounded.
**Mitigation:** paginated source-API reads with bounded page sizes + a per-run
cap; pull on a named interval (honest, not "continuous"); run timeout.

**E — Elevation of privilege.** Risk: the source key granted write/admin scope
"to be safe."
**Mitigation:** read-only least-privilege scope only (Datadog `*_read`, Grafana
Viewer); docs name the exact minimal scope per connector and warn against broad
grants. No platform-side privilege beyond push (invariant #3).

## Acceptance criteria

**Connectors — collection**

- [ ] **AC-1.** `connectors/datadog/` and `connectors/grafana/` connectors land,
      each following the slice-004 / 442 template (register-per-run, stable
      `actor_id`, `observed_at` granularity, scope minimums).
- [ ] **AC-2.** Datadog collects **monitor/alert inventory** (name, type,
      enabled state, notification-target handle) via the read-only Datadog API.
- [ ] **AC-3.** Grafana collects **alert-rule + notification-policy inventory**
      (rule, enabled state, contact-point name) via the read-only Grafana API.
- [ ] **AC-4.** Each authenticates via vendor-native read-only auth (Datadog
      read-scoped API+APP key; Grafana Viewer service-account token), documented
      as the minimum.

**Connectors — push**

- [ ] **AC-5.** Each collected record is pushed to the single `IngestEvidence`
      (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields.
- [ ] **AC-7.** Each connector registers `profiles_supported` (`pull` in v0) per
      run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** A shared monitoring/alerting-config evidence_kind (or one per
      connector) lands in the schema-registry schemas tree with
      `x-default-scf-anchors` set (OQ #9). Reuse a common shape across the two
      connectors where the field set is identical.

**Tests**

- [ ] **AC-9.** Per-connector unit/integration tests cover collect → push
      against a mocked source API (no live Datadog/Grafana in CI).
- [ ] **AC-10.** A test asserts neither connector emits a secret webhook URL /
      integration token / recipient email PII — config + target-name metadata
      only (threat-model I).
- [ ] **AC-11.** A test asserts neither connector logs its source key/token.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** Each connector README documents the minimal read-only scope,
      the pull interval, and the evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/488-monitoring-connectors-decisions.md`) records the
      shared-vs-per-connector evidence-kind choice, `x-default-scf-anchors`,
      scope-minimum, and stable-field JUDGMENT calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** Both are
  first-class peer connectors holding source-side credentials; push-only wire.
- **Licensing — no closed proprietary connectors.** OSS, in-tree, read-only API.
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** Each pull profile names its interval.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire.
- `CLAUDE.md` "Planned repository layout" — `connectors/datadog/`,
  `connectors/grafana/` named.
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.

## Anti-criteria (P0 — block merge)

- **P0-488-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-488-2.** Does NOT require or document write/admin source scopes —
  read-only least-privilege only (threat-model E).
- **P0-488-3.** Does NOT collect secret webhook URLs / integration tokens /
  recipient PII / dashboard JSON / metric time-series / log results — alert/
  monitor config + target-name metadata only (threat-model I).
- **P0-488-4.** Does NOT log or transmit a source key/token into the platform.
- **P0-488-5.** Does NOT ship a closed/proprietary collector (licensing).
- **P0-488-6.** Does NOT label a pull profile "continuous monitoring."
- **P0-488-7.** Does NOT implement Datadog-SIEM / Grafana-RBAC / firing-history
  evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; mocked source APIs) ·
`security-review` (source-credential + secret-URL over-collection risk) ·
`simplify` · `changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** both connectors are the slice-004 / 442 pattern;
  cluster them because the evidence shape is identical (alert/monitor config
  inventory). The defining risk is over-collection of secret-bearing
  notification configs — collect target _names_, never target _secrets_.
- **JUDGMENT call: shared vs per-connector evidence_kind.** Prefer one shared
  `monitoring.alert_config.v1` shape if the field set is genuinely identical;
  split only if the two vendors' alert models diverge enough to make a shared
  shape lossy. Record the call in the decisions log.
- **Other JUDGMENT calls you own:** field shapes, `x-default-scf-anchors`, scope
  minimum per connector. Record them; the maintainer re-checks anchor accuracy
  (OQ #9 load-bearing).
- Reuse `feedback_connector_patterns` conventions across both connectors.
- Detection-tier: `none` unless a bug surfaces during the build.
