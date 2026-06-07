# 489 — PagerDuty connector (incident-response evidence)

**Cluster:** Connectors
**Estimate:** S-M (1d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors (`aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual`; canvas §10.1, `connectors/`); the
planned layout (`CLAUDE.md`, "Planned repository layout") names `pagerduty` in
the alerting/incident tier. For the platform's persona — a SaaS startup security
leader — **incident-response evidence is a directly load-bearing demand**: the
v1 IR plan (slice 372) and the SOC 2 CC7.3/CC7.4/CC7.5 criteria require proof
that on-call coverage exists and that incidents are detected, escalated, and
resolved. "Show us your on-call schedule and your incident history" is a
recurring auditor ask that today requires manual upload — and PagerDuty is where
that evidence lives for most SaaS startups.

This slice ships **one vertical PagerDuty connector** following the slice-004 /
442 template (stable `actor_id`, stable optional fields, `observed_at`
granularity, register-per-run, scope minimums, vendor-native auth;
`feedback_connector_patterns`): collect **escalation-policy + on-call schedule
coverage** evidence (who is on-call, escalation tiers — coverage facts, not
personal contact details) + **incident summary** evidence (incident id, urgency,
created/resolved timestamps, status, service — NOT the incident's free-text
notes / customer data) via the read-only PagerDuty REST API, register
`profiles_supported` per run, and `Push` each record to the single
`IngestEvidence` API.

**Scope discipline.** **One connector, two evidence surfaces** (on-call coverage

- incident summaries), the minimum that demonstrates the IR-evidence connector
  is a real first-class peer. It does **not** collect incident note bodies /
  postmortem free-text / responder PII beyond on-call identity (follow-on), does
  **not** ship a webhook/event-driven profile (pull-profile only in v0 — honest
  interval), and does **not** change the platform-side wire (push-only — invariant
  #3). **Follow-on slices:** postmortem/retrospective evidence; responder
  performance metrics; event-driven profile via PagerDuty webhooks (incident
  lifecycle events).

## Threat model (STRIDE) — connector family (source-credential heavy)

A connector is a separate process holding **source-side credentials** (a
PagerDuty read-only REST API token). The dominant risks are credential handling
(over-broad token, token leakage), over-collection (incident bodies can contain
customer data + responder PII), and keeping the platform wire push-only.

**S — Spoofing.** The connector authenticates TO the platform via its push
credential (the existing connector auth — OAuth client_credentials per slice 191) and TO PagerDuty via a REST API token. Risk: a stolen push credential, or a
PagerDuty token with write scope.
**Mitigation:** push reuses the existing connector credential boundary; PagerDuty
auth uses a **read-only** REST API token (PagerDuty's read-only token type),
documented as the required minimum. The token stays source-side; the platform
never sees it (invariant #3).

**T — Tampering.** Evidence records carry a sha256 content-hash.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); ingest validates the hash. The connector does not accept inbound
data — it only reads PagerDuty + pushes.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
record carries a stable `actor_id` (the pagerduty connector + run context) and
`observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure.** Incident records can embed customer data,
sensitive triage notes, and responder PII. Risk: the connector copies an
incident's free-text body or a responder's personal phone/email into an evidence
record, or logs the token.
**Mitigation:** the connector collects **coverage + incident-summary metadata**
— escalation-policy structure, on-call assignment (the on-call identity needed
to prove coverage), incident id/urgency/status/timestamps/service — NOT incident
note bodies, NOT postmortem free-text, NOT responder personal contact details. A
test asserts no incident free-text body / personal contact detail enters an
evidence record. The token is never logged.

**D — Denial of service.** A high-volume PagerDuty account (thousands of
incidents) could make a run unbounded.
**Mitigation:** paginated REST reads with bounded page sizes + a per-run cap + a
bounded incident look-back window; pull on a named interval (honest, not
"continuous"); run timeout.

**E — Elevation of privilege.** Risk: the PagerDuty token granted write/admin
scope "to be safe."
**Mitigation:** read-only token only; docs name the exact minimal token type and
warn against write/admin tokens. No platform-side privilege beyond push
(invariant #3).

## Acceptance criteria

**Connector — collection**

- [ ] **AC-1.** A `connectors/pagerduty/` connector lands following the
      slice-004 / 442 template (register-per-run, stable `actor_id`,
      `observed_at` granularity, scope minimums).
- [ ] **AC-2.** It collects **escalation-policy + on-call schedule coverage**
      evidence via the read-only PagerDuty REST API.
- [ ] **AC-3.** It collects **incident summary** evidence (id, urgency, status,
      created/resolved timestamps, service) over a bounded look-back window via
      the read-only PagerDuty REST API.
- [ ] **AC-4.** It authenticates via a least-privilege read-only PagerDuty REST
      API token, documented as the minimum.

**Connector — push**

- [ ] **AC-5.** Each collected record is pushed to the single `IngestEvidence`
      (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields.
- [ ] **AC-7.** The connector registers `profiles_supported` (`pull` in v0) per
      run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** The on-call-coverage + incident-summary evidence_kind schemas
      land in the schema-registry schemas tree with `x-default-scf-anchors` set
      (OQ #9).

**Tests**

- [ ] **AC-9.** Connector unit/integration tests cover collect → push against a
      mocked PagerDuty API (no live PagerDuty in CI).
- [ ] **AC-10.** A test asserts the connector emits NO incident free-text body /
      postmortem text / responder personal contact detail — coverage + summary
      metadata only (threat-model I).
- [ ] **AC-11.** A test asserts the connector never logs the PagerDuty token.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** A connector README documents the minimal read-only token, the
      pull interval, the incident look-back window, and the evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/489-pagerduty-connector-decisions.md`) records the
      evidence-kind shape, the on-call-identity-vs-PII boundary, the look-back
      window, scope-minimum, and stable-field JUDGMENT calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** First-class
  peer connector holding source-side credentials; push-only platform wire.
- **Licensing — no closed proprietary connectors.** OSS, in-tree, read-only API.
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** The pull profile names its interval.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire.
- `CLAUDE.md` "Planned repository layout" — `connectors/pagerduty/` named.
- `docs/issues/372-*` (IR plan) — the IR-evidence demand this connector serves.
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.
- **#372** (IR plan) — `merged`. The IR-evidence demand context; NOT a code dep.

## Anti-criteria (P0 — block merge)

- **P0-489-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-489-2.** Does NOT require or document write/admin PagerDuty tokens —
  read-only least-privilege only (threat-model E).
- **P0-489-3.** Does NOT collect incident free-text bodies / postmortem text /
  responder personal contact detail — coverage + summary metadata only
  (threat-model I).
- **P0-489-4.** Does NOT log or transmit the PagerDuty token into the platform.
- **P0-489-5.** Does NOT ship a closed/proprietary collector (licensing).
- **P0-489-6.** Does NOT label the pull profile "continuous monitoring."
- **P0-489-7.** Does NOT implement postmortem / responder-metrics / event-driven
  evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; mocked PagerDuty API) ·
`security-review` (source-credential + incident-body over-collection risk) ·
`simplify` · `changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** the connector is the slice-004 / 442 pattern; the
  defining risk is over-collection of incident bodies (customer data + responder
  PII). Collect the on-call identity needed to prove coverage and the incident
  _summary_ fields only — never the free-text body.
- **JUDGMENT calls you own:** evidence-kind field shapes, the on-call-identity
  vs PII boundary, the incident look-back window default, `x-default-scf-anchors`
  per kind, scope minimum. Record in the decisions log; the maintainer re-checks
  anchor accuracy (OQ #9 load-bearing).
- Reuse `feedback_connector_patterns`: stable actor_id, stable optional fields,
  observed_at granularity, register-per-run, scope minimums, vendor-native auth.
- Detection-tier: `none` unless a bug surfaces during the build.
