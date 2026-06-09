# PagerDuty connector

The PagerDuty connector (slice 489) brings **incident-response evidence** into
the platform's evidence pipeline — the recurring SOC 2 CC7.3 / CC7.4 / CC7.5
("the entity detects, responds to, and resolves security events") auditor demand
and the slice-372 IR plan's "show your on-call schedule and incident history"
ask. It follows the locked connector pattern verbatim: register-per-run, a
stable `actor_id`, an hour-truncated `observed_at`, scope minimums, and
vendor-native read-only auth. It emits three evidence kinds:

| Kind                              | Profile | Source                                              |
| --------------------------------- | ------- | --------------------------------------------------- |
| `pagerduty.oncall_coverage.v1`    | pull    | PagerDuty REST API `GET /escalation_policies`       |
| `pagerduty.incident_summary.v1`   | pull    | PagerDuty REST API `GET /incidents?since=&until=`   |
| `pagerduty.postmortem_summary.v1` | pull    | PagerDuty REST API `GET /postmortems?since=&until=` |

The postmortem-summary kind (slice 538) is the deliberate slice-489 follow-on
(P0-489-7): it carries postmortem / retrospective **METADATA only** (existence,
status, timestamps, corrective-action count + completed/open rollup), feeding
SOC 2 CC7.5 ("recover from identified security incidents") and the slice-372 IR
continuous-improvement loop — **never** the narrative. See the boundary section
below.

The connector is **API-based**, not an in-host agent — consistent with the "no
closed proprietary collector agents" anti-pattern. It reads the read-only
PagerDuty REST API. The PagerDuty token stays source-side and never enters an
evidence record or a platform push (canvas invariant #3).

## The coverage-and-summary boundary (the load-bearing guard)

Incident records can embed customer data, sensitive triage notes, and responder
PII. The connector collects **coverage facts + incident-summary metadata only**:

**Collected (in scope):**

- escalation policies, their tiers, and the on-call **identity** needed to prove
  coverage — the on-call user/schedule's opaque id + display name;
- incident **id / number / urgency / status / service / created+resolved
  timestamps** over a bounded look-back window;
- per-postmortem **META-FACTS**: that a review **exists** for an incident, the
  linked incident id, the review **status**, the **created / published
  timestamps**, and the corrective-action **COUNT** + completed/open rollup.

**Never collected (out of scope):**

- a responder's personal **phone number, personal email, or any personal
  contact detail** (on-call identity is in scope; contact details are not);
- the incident's free-text **title / body / notes** (which can embed customer
  data);
- the postmortem **narrative body, timeline, or root-cause prose**, an action
  item's operator-authored **title / description**, or any customer data /
  responder PII the postmortem free-text embeds.

The decode boundary is the enforcement point: the HTTP clients decode only the
listed fields into PII-free / free-text-free structs, so the unwanted fields
never enter memory as connector data — even when the PagerDuty payload carries
them, because `json.Decode` discards JSON keys with no matching struct field.

For the postmortem kind the over-collection risk is **dominant** (a postmortem
is dense free-text), so the boundary is also enforced **structurally**: the
record types (`RawPostmortem` / `Postmortem` / `RawActionItem`) have **no field
that can hold the narrative or an action-item title BY CONSTRUCTION**, and a
reflection guard (`postmortems_guard_test.go:TestMetadataOnly_StructuralGuard`)
**fails the build** the moment such a field is added. A drop test
(`postmortems/client_test.go:TestClient_ListPostmortems_DropsNarrative`) feeds a
fake API response that deliberately embeds a narrative body, root-cause prose,
responder PII, and action-item titles, and proves none of it reaches a record.

A test (`integration_test.go:TestEmittedRecords_NoPIIorFreeText`) asserts no
PII-shaped or free-text substring reaches an emitted payload (AC-10), and
another asserts the token is never logged (AC-11).

## Least-privilege token (required minimum)

Set `PAGERDUTY_TOKEN` to a **read-only** PagerDuty REST API token. Run
`atlas-pagerduty permissions` to print the canonical minimum.

- Use a **read-only** REST API key.
- **NEVER** use a full-access / write / admin token — the connector issues only
  read `GET`s against `/escalation_policies`, `/incidents`, and `/postmortems`.
  No new credential scope beyond the slice-489 read-only token is required.

The token is read from the environment, never a CLI flag (so it never lands in
shell history), and is never logged or placed into an evidence record.

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly. An event-driven profile (PagerDuty
incident-lifecycle webhooks) is a documented follow-on, not part of v0.

## Incident look-back window

`--lookback-days` (default **90**) bounds the incident **and postmortem** query
windows (`since = now - lookback-days`, `until = now`). 90 days fits a typical
SOC 2 observation increment while keeping each run bounded (threat-model D).
Increase it for a wider audit window; the run cost grows with the incident /
postmortem count. The postmortem read is additionally hard-capped
(`postmortems.MaxRecords`) and the page loop is bounded, so a paginated source
cannot make a run unbounded.

## Usage

```sh
# Print the least-privilege token requirement.
atlas-pagerduty permissions

# Register the connector instance (profiles_supported = [pull]).
export SECURITY_ATLAS_ENDPOINT=atlas.example.com:443
export SECURITY_ATLAS_TOKEN=<platform bearer>
atlas-pagerduty register

# Read coverage + incident summaries + postmortem metadata and push evidence.
export PAGERDUTY_TOKEN=<read-only PagerDuty REST API token>
atlas-pagerduty run --environment prod --lookback-days 90
```

The `run` subcommand emits all three kinds in one pass. Override the
postmortem control mapping with `--postmortem-control` (default `scf:IRO-13`).

## Scope minimums

Every record is scoped to `service` (default `pagerduty`) and the required
`--environment`. Records carry `Result = INCONCLUSIVE`: the connector reports a
descriptive coverage / incident posture; the platform evaluator owns the final
pass/fail per `(control, scope)`.

## Default SCF anchors (maintainer recheck — OQ #9)

The bundled schemas carry default SCF-anchor hints, flagged for maintainer
accuracy recheck:

- `pagerduty.oncall_coverage.v1` → `IRO-04` (Incident Response Plan), `IRO-07`
  (Incident Response Team) — proves an IR capability exists and is staffed.
- `pagerduty.incident_summary.v1` → `IRO-02` (Incident Handling), `IRO-09`
  (Incident Reporting) — proves incidents are detected, handled, and resolved.
- `pagerduty.postmortem_summary.v1` → `IRO-13` (Root-Cause Analysis), `IRO-09`
  (Incident Reporting) — proves incidents are reviewed and corrective actions
  tracked (SOC 2 CC7.5; the slice-372 IR continuous-improvement loop).

## Follow-ons (out of v0 scope)

- responder-performance metrics (slice 539);
- event-driven profile via PagerDuty webhooks (slice 540).
