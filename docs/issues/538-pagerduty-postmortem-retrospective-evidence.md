# 538 — PagerDuty connector: postmortem / retrospective evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + redaction story + over-collection boundary)
**Status:** `blocked` (depends on #489 — base PagerDuty connector — merged first)

## Narrative

Slice 489 shipped the PagerDuty connector with two pull-only surfaces — on-call
coverage (`pagerduty.oncall_coverage.v1`) and bounded-window incident summaries
(`pagerduty.incident_summary.v1`). It deliberately deferred **postmortem /
retrospective evidence** as a follow-on (P0-489-7) because postmortems are dense
free-text that routinely embed customer data, responder PII, and root-cause
narrative — the exact over-collection risk slice 489's coverage-and-summary
boundary was built to avoid.

Postmortem evidence is nonetheless load-bearing for SOC 2 CC7.5 ("the entity
implements activities to recover from identified security incidents") and for the
slice-372 IR plan's continuous-improvement loop: an auditor wants proof that
incidents are reviewed and that corrective actions are tracked, not the raw
narrative.

This slice adds a postmortem **metadata** surface: per-postmortem
existence/completion, the linked incident id, status (e.g. in-progress /
published), created/published timestamps, and the count of tracked action items
— NOT the postmortem free-text, the timeline narrative, or any customer/PII
content. The hard JUDGMENT this slice owns is the **redaction story**: what, if
any, structured fields (e.g. action-item titles) can be collected without
pulling free-text, and whether action-item titles are themselves operator-
authored free-text that must be excluded.

## Threat model

Source-credential-heavy, plus a DOMINANT incident-narrative over-collection risk
that 489's summary-only surface deliberately avoided.

- **S — Spoofing.** Platform push via the existing connector credential; source
  via the read-only PagerDuty REST token. **Mitigation:** read-only
  least-privilege token (the 489 minimum); source creds stay source-side
  (invariant #3).
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** Register-per-run + stable `actor_id`
  (`connector:pagerduty:postmortem@<version>`) + `observed_at` granularity.
- **I — Information disclosure (DOMINANT).** Postmortems embed customer data +
  responder PII + root-cause narrative. **Mitigation:** collect postmortem
  metadata only (existence/status/timestamps/action-item COUNT + linked incident
  id) — never the narrative, timeline free-text, or action-item free-text unless
  the redaction JUDGMENT explicitly allows a bounded structured field. A test
  asserts no postmortem free-text / customer data / PII enters a record.
- **D — Denial of service.** Bound the look-back window + per-run cap + run
  timeout (the 489 pattern).
- **E — Elevation of privilege.** Read-only token only; no write/admin.

## Acceptance criteria (sketch — refine at pickup)

- [ ] A postmortem-metadata collector lands following the slice-489 pattern.
- [ ] The redaction JUDGMENT is made and documented (which structured fields, if
      any, beyond existence/status/timestamps/action-item count).
- [ ] Pushes to the single `IngestEvidence` (`Push`) API — no wire change.
- [ ] `pagerduty.postmortem_summary.v1` evidence kind + schema with
      `x-default-scf-anchors` (candidate: `IRO-13` Root Cause Analysis /
      `IRO-09`), registered in `DefaultSeed`.
- [ ] Tests cover collect → push against a mocked PagerDuty API (no live source).
- [ ] A test asserts no postmortem free-text / customer data / PII enters a
      record; a test asserts the token is never logged.
- [ ] README + decisions log + changelog.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No write/admin PagerDuty token.
- **P0.** No postmortem narrative / timeline free-text / customer data / PII in a
  record.
- **P0.** No token logged or transmitted into the platform.
- **P0.** No "continuous monitoring" mislabel; the profile name is honest.

## Dependencies

- **#489** (base PagerDuty connector) — provides the connector scaffold, the
  `pagerdutyauth` + `pdhttp` packages, and the IR-evidence family.
