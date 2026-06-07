# 533 — Datadog connector: Cloud-SIEM / Security-Monitoring rule evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #488 — base Datadog connector — merged first)

## Narrative

Slice 488 shipped the base Datadog connector with one evidence surface — monitor
/ alert configuration inventory (`monitoring.alert_config.v1`) — deliberately
scoped to the minimum that proves the monitoring evidence family is a first-class
peer. It explicitly deferred Datadog **Cloud-SIEM (Security Monitoring) detection
rules** as a follow-on (P0-488-7).

This slice adds that surface: read Datadog Security-Monitoring rule inventory
(rule name, type — log/signal correlation/threshold, enabled state, severity, and
the notification-target HANDLE) via the read-only Datadog Security-Monitoring API
(`GET /api/v2/security_monitoring/rules`, `security_monitoring_rules_read`
scope). The recurring SOC 2 CC7.2/CC7.3 and ISO A.12 evidence demand is "prove
threat-detection rules are configured"; this is distinct from the operational
monitors covered in 488.

This is the slice-488 pattern verbatim: a new `internal/siemrules` collector + a
new evidence kind (JUDGMENT: likely a sibling `datadog.siem_rule.v1` rather than
reusing `monitoring.alert_config.v1`, because a detection rule carries
severity + a detection query-class field that the alert-config shape lacks — see
slice 488 decisions D1, which reserved exactly this case for a split) + its schema
with `x-default-scf-anchors` (candidate: `MON-01` + `THR-01` Threat
Intelligence/Detection), registered in `DefaultSeed`, faked Datadog API surface in
tests. No platform-side wire change (invariant #3 — push only);
`profiles_supported` stays `[pull]`; the interval stays honestly named.

**Scope discipline.** SIEM rule configuration only. It does NOT read firing
signals, the detection query's raw log samples, or matched-event payloads.

## Threat model (STRIDE)

Same source-credential-heavy shape as slice 488. The dominant risks are
unchanged: over-broad source scope, key leakage, and over-collection.

- **S — Spoofing.** Connector authenticates to the platform via its existing push
  credential and to Datadog via the key pair. **Mitigation:** read-only
  `security_monitoring_rules_read` scope only; source keys stay source-side.
- **T — Tampering.** Records carry a sha256 content-hash; ingest validates it.
- **R — Repudiation.** Register-per-run + stable `actor_id`
  (`connector:datadog:siemrules@<version>`) + documented `observed_at`
  granularity.
- **I — Information disclosure.** A detection rule can embed sensitive query
  expressions and route to secret-bearing notification targets.
  **Mitigation:** collect rule name / type / severity / enabled + notification
  target HANDLE only — never the secret webhook URL, integration token, recipient
  PII, the raw detection query, or matched signals/events. A test asserts no
  secret / query / signal payload enters a record. Source keys never logged.
- **D — Denial of service.** Bounded paginated reads + a per-run cap + honest
  pull interval + run timeout.
- **E — Elevation of privilege.** Read-only least-privilege scope only; docs name
  the exact minimum and warn against broad grants.

## Acceptance criteria (sketch — refine at pickup)

- [ ] `connectors/datadog/internal/siemrules` collector lands following the
      slice-488 pattern (register-per-run, stable `actor_id`, `observed_at`
      granularity, scope minimum).
- [ ] Collects Datadog Security-Monitoring rule inventory via the read-only API.
- [ ] Authenticates via the read-only `security_monitoring_rules_read` scope,
      documented as the minimum.
- [ ] Pushes to the single `IngestEvidence` (`Push`) API — no wire change.
- [ ] sha256 content-hash + stable optional fields; `profiles_supported=[pull]`,
      honest interval.
- [ ] Evidence kind + schema with `x-default-scf-anchors` (JUDGMENT:
      split vs shared — record the call).
- [ ] Tests cover collect → push against a mocked source API (no live Datadog).
- [ ] A test asserts no secret URL / token / recipient PII / raw query / signal
      enters a record; a test asserts the source key is never logged.
- [ ] README + decisions log + changelog.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No write/admin source scope.
- **P0.** No secret webhook URLs / tokens / recipient PII / raw detection queries
  / firing signals / matched events.
- **P0.** No source key/token logged or transmitted into the platform.
- **P0.** Read-only OSS API; honest pull interval.

## Dependencies

- **#488** (base Datadog connector) — provides the connector scaffold, auth, and
  the shared monitoring evidence family.
