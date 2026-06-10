# Slice 636 — Datadog Cloud-SIEM signal-history — decisions log

Type: JUDGMENT. This log records the subjective build-time calls. It does NOT
block merge; the maintainer iterates post-deployment from the Revisit list.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice — a clean structural-sibling feature slice.)

## Context

Slice 533 added `datadog.siem_rule.v1` — detection-rule **configuration**
(which rules exist: class, severity, enabled, routing) for SOC 2 CC7.2
(detection design). This slice adds the orthogonal CC7.3 (incident _response_)
surface: a record per Cloud-SIEM **signal** that actually fired and its triage
outcome — "rules fired AND were triaged, when, by whom." New evidence kind
`datadog.siem_signal.v1`. Sourced read-only from Datadog's security-signals
search API (`GET /api/v2/security_monitoring/signals`, scope
`security_monitoring_signals_read`).

## Decisions made

### D1 — Over-collection field boundary (the load-bearing JUDGMENT) — confidence: high

A signal-triage record carries triage METADATA only. Exactly these fields are
IN; everything else is structurally OUT (the record + raw structs physically
cannot hold the excluded data — the slice-533 guard template, mirrored here as
`TestNormalize_DropsSignalBodyPayload`):

IN (answers "did rules fire and were they triaged, when, by whom"):

- `signal_id` — Datadog-native opaque signal id (non-secret).
- `rule_id` — the firing detection rule's id (ties the signal back to the
  slice-533 `datadog.siem_rule.v1` configuration record).
- `rule_name` — human-readable rule name (descriptive label, not signal body).
- `severity` — info/low/medium/high/critical (the rule's configured severity
  reflected on the signal — a label, never the matched content).
- `status` — the triage state, normalized to the API's workflow enum:
  `open` / `under_review` / `archived` (Datadog's `triage_state`). We map the
  three Datadog states to a slightly richer audit vocabulary that also accepts
  `triaged` / `closed` aliases for forward-compat, normalizing unknown states
  to `open` (conservative — an un-triaged signal is the audit-relevant case).
- `first_seen_at` / `triaged_at` / `last_updated_at` — the timeline timestamps
  that prove a triage actually happened within the audit period.
- `triager_handle` — the OPAQUE handle/id of who triaged (the `archived_by` /
  triage actor). A handle, never a raw email or display name (PII). When the
  source returns an email-shaped actor, it is DROPPED (mirrors the slice-533
  email-recipient PII drop), never emitted.

OUT (structurally impossible — no struct field can hold them):

- The matched log/event samples (`samples`, `attributes.custom`,
  `message`-embedded log lines).
- The raw detection query / the rule's query text.
- The signal `message` body (free-text narrative — may carry PII / customer
  data / IPs / hostnames / payloads).
- Tags / facets that carry user identities, source IPs, or asset hostnames
  (signal-body PII). The decode struct simply does not read `tags`,
  `attributes.custom`, `samples`, or `message`.

Rationale: this is the canvas anti-pattern boundary (connector over-collection
discipline) plus the slice-533 P0 structural guard. A signal MESSAGE and its
matched SAMPLES are the highest-PII surface in the Datadog SIEM data model — a
record that proves "triage happened" needs none of it. The guard is the type
system: a guard test feeds a fixture containing `message`, `samples`, a raw
`query`, and a `user.email` tag and asserts none reaches an emitted record.

### D2 — Profile shape + honest naming — confidence: high

Chosen: **bounded PULL of the last-N-hours of signals** via the security-signals
search API, on the same operator-scheduled cadence as slice 533. `--lookback`
(default 24h) names the window honestly. `profiles_supported` stays `[pull]`.

Rejected: a genuine `subscribe`/webhook-receipt profile. Datadog's
security-signals API is a **search/poll** API; Datadog does not offer a
first-class push the connector receives for security signals (Datadog routes
signal notifications via the rule's notification handles — Slack/PagerDuty/
webhook integrations the OPERATOR owns, not an inbound feed to this connector).
Standing up a novel inbound webhook receiver would (a) violate the lower-risk
in-scope default the spec strongly recommends and (b) require a SOURCE-side
receiver with no real Datadog push behind it. The spec filename says
"event-driven"; the spec BODY explicitly permits and strongly defaults to the
bounded-pull option when no first-class push exists. We take the default.

Honest-naming discipline (P0): the run command, README, and register profile all
name this a bounded look-back **pull**, NOT "continuous monitoring" and NOT
"event-driven." The platform-side wire is push regardless (invariant #3); the
connector emits each normalized signal via the one `Push` RPC.

### D3 — Anchor choice — confidence: medium

Chosen `x-default-scf-anchors`: **THR-01** + **IRO-09**.

- THR-01 (Threat Intelligence Program / the SIEM-detection anchor seeded by
  slice 635/641) ties the signal record to its slice-533 rule-configuration
  sibling — same detection lineage.
- IRO-09 (Incident Reporting) is the closest in-repo anchor to the CC7.3
  incident-response demand this surface serves (a triaged signal IS a reported/
  handled detection). Verified real + in-repo (`migrations/fixtures/
scf-sample.json`).

Rejected IRO-02 (Incident Handling): the spec named it as a candidate, but
**IRO-02 is NOT in the bundled SCF catalog fixture** (only IRO-04 + IRO-09 are
seeded). Adopting an anchor that does not resolve in-repo would be a dangling
reference. IRO-09 is the closest real incident-response anchor and is honest.

### D4 — Reuse vs new package — confidence: high

New `internal/siemsignals` package (sibling to `internal/siemrules`), new
`datadog.siem_signal.v1` kind, new `security_monitoring_signals_read` scope
method on `datadogauth`. NOT a widening of `datadog.siem_rule.v1`: a signal
(fired instance + triage state + actor) is structurally distinct from a rule
(configuration). This mirrors the slice-488→533 sibling-split precedent exactly.

## Revisit once in use

- D3 (medium): re-confirm THR-01 + IRO-09 are the auditor-preferred anchors once
  a real SOC 2 CC7.3 sample request lands. If a CC7.3-specific SCF anchor
  (IRO-02 Incident Handling) is later imported into the bundled catalog, prefer
  it over (or alongside) IRO-09.
- D1: re-check the `status` enum mapping once real Datadog signal payloads are
  observed — confirm Datadog's live `triage_state` values match the assumed
  `open`/`under_review`/`archived` set; widen the normalizer if a new state
  appears (it defaults to `open`, the safe audit case, until then).
- D2: re-check the default 24h `--lookback` against real signal volume — if a
  busy tenant produces more than `maxPages*pageSize` signals in 24h, the cap
  triggers honestly; the operator narrows the window or raises the cap
  deliberately (same DoS-guard shape as slice 533).
- D1: if a future demand needs the triage NOTE/disposition reason, it is a
  SEPARATE deliberate slice with its own PII review — do not widen this record.
