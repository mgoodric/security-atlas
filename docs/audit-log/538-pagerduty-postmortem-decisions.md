# 538 — PagerDuty postmortem/retrospective evidence: JUDGMENT decisions log

Slice type: JUDGMENT (evidence-kind shape + redaction story + over-collection
boundary). This file records the subjective build-time calls for slice 538 — the
evidence-kind shape, the redaction / over-collection-boundary mechanism (HOW the
postmortem narrative can't leak), the SCF anchor choice, and the stable-field
choices. It does NOT block merge; the maintainer iterates post-deployment from
the "Revisit once in use" notes.

Parent: slice 489 (`docs/issues/489-*` + the base PagerDuty connector under
`connectors/pagerduty/**`). Slice 489 shipped the connector emitting
`pagerduty.oncall_coverage.v1` + `pagerduty.incident_summary.v1` and
deliberately deferred postmortem evidence (P0-489-7) precisely because a
postmortem is dense free-text embedding customer data, responder PII, and
root-cause narrative — the exact over-collection risk 489's coverage-and-summary
boundary was built to avoid. This slice adds the postmortem surface as
METADATA-only.

## D1 — Evidence-kind shape: sibling kind `pagerduty.postmortem_summary.v1`

- **Options considered:** (a) enrich the existing `pagerduty.incident_summary.v1`
  record with postmortem fields; (b) a new sibling kind
  `pagerduty.postmortem_summary.v1` carrying one record per postmortem.
- **Chosen:** (b), the sibling kind — as the slice doc's AC sketch anticipated.
- **Rationale:**
  1. **Different lifecycle + altitude.** An incident summary is a per-incident
     operational fact (status, urgency, timestamps). A postmortem is a _review
     artifact_ with its own lifecycle (draft → in-review → published) and its own
     corrective-action rollup. Not every incident has a postmortem; a postmortem
     can be revised after the incident resolves. Bolting postmortem fields onto
     the incident record would conflate two altitudes and force a
     review-state change to be attributed to an incident-summary push.
  2. **Independent evaluation + control mapping.** Incident-summary feeds
     IRO-02/IRO-09 (handling/reporting); postmortem feeds IRO-13/IRO-09
     (root-cause / continuous improvement, SOC 2 CC7.5). A sibling kind lets the
     evaluator query the review surface as its own thing.
  3. **Clean append-only ledger (invariant #2).** Distinct idempotency-key
     prefix (`pagerduty.postmortem_summary|...`) so the two kinds never collide.
  4. **Repo precedent.** Mirrors the slice-488→533 / 490→555 / 489→538 sibling
     splits: when a follow-on surface carries fields the base shape has no slot
     for, it gets its own kind.
- **Shape:** one record per postmortem. Payload keys (all metadata):
  `postmortem_id`, `incident_id`, `status`
  (`not_started`/`in_progress`/`in_review`/`published`), `created_at`, optional
  `published_at`, `action_item_count`, `action_items_completed`,
  `action_items_open`. `additionalProperties:false`; `required` = the seven
  non-optional keys. `Result = INCONCLUSIVE` (the connector reports a descriptive
  review posture; the platform evaluator owns the pass/fail per (control,scope)).

## D2 — The redaction / over-collection boundary (THE load-bearing decision)

This is the central discipline of the slice. A postmortem body embeds customer
data, responder PII, and root-cause prose. The question this slice owns: _what,
if any, structured fields can be collected without pulling free-text?_

- **Decided IN:** existence (`postmortem_id`), the linked `incident_id`, the
  review `status`, the `created_at` / `published_at` timestamps, and the
  corrective-action ROLLUP — the _count_ of action items plus the completed/open
  split.
- **Decided OUT (never collected):**
  - the postmortem **narrative body**, **timeline** free-text, **root-cause**
    prose, and any **notes** field;
  - an action item's **title / description**. This is the sharpest sub-call:
    action-item titles are operator-authored free-text that routinely name the
    affected customer, system, or root cause ("Email ACME about the SSN
    exposure"). They are therefore EXCLUDED — only the item's _completion state_
    is read, and only to compute the count rollup;
  - any customer data or responder PII the free-text embeds.
- **HOW the narrative can't leak (the mechanism — three layers):**
  1. **Structural / by-construction (primary).** The connector-side record types
     `RawPostmortem`, `Postmortem`, and `RawActionItem` have NO field capable of
     holding narrative free-text or an action-item title. `RawActionItem` is a
     single `Completed bool`. If the struct physically can't hold the narrative,
     the narrative can't be emitted. This is the same boundary slice 489's
     incident/oncall types use ("no Title/Body field BY CONSTRUCTION").
  2. **Decode-discard.** The HTTP decode struct (`apiPostmortems`) omits the
     `body`/`narrative`/`timeline`/`root_cause`/`notes` JSON keys and the action
     item's `title`/`description`. `json.Decode` discards JSON keys with no
     matching struct field, so even though the PagerDuty payload carries the
     narrative, it never enters memory as connector data.
  3. **Reflection tripwire (regression net).** `TestMetadataOnly_StructuralGuard`
     walks every field of every record type by reflection and FAILS THE BUILD if
     any field name contains a free-text/PII token (`body`, `narrative`, `title`,
     `description`, `cause`, `email`, `name`, …) or if any unexpected `string`
     field appears (only opaque-id/status strings are allow-listed). Verified the
     tripwire is real: adding a `Body string` field makes the test go red. This
     keeps a future "just a bit more context" change from silently widening the
     boundary.
- **Executable proof.** `TestClient_ListPostmortems_DropsNarrative` feeds a fake
  PagerDuty response that DELIBERATELY embeds a narrative body, a root-cause
  blob, responder PII (`123-45-6789`, `jane@acme.example`, `+1-555-0100`), and
  action-item titles, then stringifies the entire decoded result and asserts none
  of those substrings is present. The integration test
  `TestEmittedRecords_NoPIIorFreeText` extends the same allow-list assertion to
  the emitted record payload.

## D3 — SCF anchors: `IRO-13` (Root-Cause Analysis) + `IRO-09` (Incident Reporting)

- **Chosen:** `x-default-scf-anchors=[IRO-13, IRO-09]`.
- **Rationale:** the postmortem surface's auditor value is proving incidents are
  _reviewed_ and corrective actions _tracked_ — SOC 2 CC7.5 ("the entity
  identifies, develops, and implements activities to recover from identified
  security incidents") and the slice-372 IR plan's continuous-improvement loop.
  `IRO-13` (Root-Cause Analysis) is the canonical SCF anchor for the
  review-and-learn step; `IRO-09` (Incident Reporting) is shared with the
  incident-summary kind and covers the reporting-of-outcomes facet. The slice
  doc's AC sketch floated `IRO-13` / `IRO-09` as the candidate; this confirms it.
- **Caveat:** like every connector's anchors these are DEFAULT mapping hints
  flagged for maintainer accuracy recheck (OQ #9). The `run` subcommand's
  `--postmortem-control` defaults to `scf:IRO-13` and is operator-overridable.

## D4 — No new credential scope; reuse the slice-489 read-only token

- The read-only PagerDuty REST API key slice 489 already requires reads
  `/postmortems` exactly as it reads `/incidents` — no new scope, no write/admin
  token (P0). The `pagerdutyauth` + `pdhttp` packages are reused unchanged, so
  there are NO new shared-package lines to cover (the b228 coverage-ratchet
  concern about a new-auth-helper-at-0% does not apply here — nothing new was
  added to a shared package). The `permissions` subcommand was updated to
  document the `/postmortems` GET and the postmortem narrative as never-read.

## D5 — Bounded reads (DoS guard, threat-model D)

- The postmortem read uses the SAME bounded look-back window as the incident
  read (`--lookback-days`, default 90). On top of that the client paginates a
  BOUNDED page loop (`maxPages`) and the whole run is hard-capped at
  `postmortems.MaxRecords` (1000). A paginated or pathological source that claims
  `more:true` forever stops at the cap rather than reading without bound
  (`TestClient_ListPostmortems_StopsAtRunCap` + `TestCollect_HardCap`). The
  per-push 10s timeout and 20s HTTP client timeout from 489 apply unchanged.

## D6 — Stable-field choices (idempotency, actor_id, scope, observed_at)

- **Idempotency key:** `sha256("pagerduty.postmortem_summary|<postmortem_id>|<hour>")`
  — collapses same-postmortem re-runs within the hour into one ledger row,
  mirroring the oncall/incident key shape.
- **actor_id:** `connector:pagerduty:postmortems@<version>` (the locked
  cross-connector convention).
- **scope:** `service` (default `pagerduty`) + required `--environment`, as the
  other two kinds.
- **observed_at:** hour-truncated, shared across the run.
- **content-hash:** sha256 per record (the platform ingest computes it; the
  connector emits the canonical record).

## Detection-tier classification

- `detection_tier_actual`: `none` — no defect surfaced during the slice. The
  over-collection boundary was designed structurally up-front, so the
  narrative-leak class of bug was prevented by construction rather than caught
  after the fact. The reflection-guard tripwire was verified (a temporary `Body`
  field made it go red) but that was a deliberate verification, not a discovered
  defect.
- `detection_tier_target`: `none`. Had a narrative-leak regression been
  introduced (e.g. a future field widening), the `target` tier is `unit` — the
  `TestMetadataOnly_StructuralGuard` reflection test + the
  `TestClient_ListPostmortems_DropsNarrative` drop test are the unit-tier nets
  that would catch it at build time, before integration or production.

## Revisit once in use

- Whether the corrective-action rollup should additionally carry a _coarse age
  bucket_ of the oldest open action item (e.g. ">90d open") — useful for the
  CC7.5 "corrective actions actually close" signal — WITHOUT pulling the item
  title. Deferred: a count + age bucket stays metadata-only, but it is one more
  derivation to justify; left for maintainer demand.
- The `IRO-13`/`IRO-09` anchor accuracy recheck (OQ #9).
- An event-driven profile (PagerDuty postmortem webhooks) instead of the bounded
  pull — the same follow-on already noted for the incident surface (slice 540).
