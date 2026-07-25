# Slice 494 — decisions log

**Slice:** 494 — Assessment-Results export: wire drawn sample evidence + walkthrough attachments
**Type:** JUDGMENT
**Branch:** `oscal/494-ar-sampled-evidence`
**Date:** 2026-06-07

This slice completes the AR exporter (`internal/oscal/aggregate.go`) so the
Assessment-Results artifact carries (1) the **drawn** sample evidence IDs per
`SamplePopulation` and (2) **walkthrough attachment references** — closing the
two placeholders called out in canvas §8.2 / §8.3 / §8.5.

---

## Decisions

### D1 — Sampled-evidence source: READ the persisted draw, do NOT recompute at export

**The slice notes framed the JUDGMENT call (c) as "persist at freeze time vs.
compute at export time from the persisted seed."** Grilling the real schema
(`migrations/sql/20260511000010_audit_samples.sql`) showed the framing is
already resolved by slice 026's design — and in a stronger way than either
option in the notes:

- The realized draw is **already materialized at draw-time** in the
  `sample_evidence` table: `(sample_id, evidence_record_id, ordinal)`, ordered by
  `ordinal` (the Fisher-Yates shuffle position). The slice-026 migration comment
  is explicit: _"Stored explicitly so a re-audit returns the same records
  regardless of any subsequent population mutations ... defensive against slice
  028's frozen_at writes mutating the universe under a sample's feet."_
- The **seed lives on the `samples` row, NOT on the `populations` row.** So the
  notes' phrasing ("compute from the population's persisted seed") is not even
  achievable as written — there is no seed column on `populations`.
- `ListPopulationEvidenceIDs` (the query the sampler ran over at draw-time)
  already filters `observed_at <= COALESCE(frozen_at, 'infinity')`. So the
  persisted `sample_evidence` rows were drawn from the **frozen population by
  construction** — the invariant-#10 horizon was applied when the draw happened
  (slice 028 set `frozen_at`, the draw honored it). Reading those rows back
  carries the frozen-correct draw forward with zero re-computation.

**Decision:** the AR exporter **reads the persisted `sample_evidence`** for each
population's most-recent sample, joining `populations → samples →
sample_evidence`. This is:

- **Correct for AC-2 / AC-7 by construction** — the persisted draw was made
  against the frozen population; a post-freeze evidence record was never in the
  draw, so it cannot appear in `sample_evidence`.
- **Cheaper** than re-shuffling (slice-028 "keep the freeze/export cheap"
  spirit) — a single ordered read, no SHA-256 + ChaCha8 + Fisher-Yates per
  export.
- **Honors P0-494-4** — it consumes slice 026's draw verbatim; it does not
  re-run or re-implement the sampler in the hot export path.
- **Reproducible for AC-9** — the persisted draw IS the output of
  `Sample(frozen_population, n, persisted_seed)`; a verifier re-running the
  sampler with `samples.seed` against the frozen population gets the same set.
  AC-9 is proven by an integration test that does exactly that re-run and
  asserts set-equality with the AR's `sampled_evidence_ids`.

**Edge cases:**

- A population with **no `samples` row** (population defined but never drawn)
  carries an empty `sampled_evidence_ids[]` — the AR honestly says "population
  of M, nothing drawn yet." This is not an error; the auditor sees the gap.
- A population with **multiple `samples`** (re-draws): the AR carries the
  **most-recent** draw (`ORDER BY samples.created_at DESC, samples.id DESC
LIMIT 1`), which is the operative sample the auditor would act on. The full
  re-draw history stays in the `sample_audit_log` (slice 026); the AR is a
  point-in-time artifact, not the audit trail.

**Confidence: HIGH.** The schema and slice-026 comments make this the obviously
correct reading; the alternative (recompute) would re-derive data that is
already pinned, risking divergence if the sampler ever changed.

### D2 — Walkthrough-attachment OSCAL mapping: `observation.relevant-evidence[]`, NOT back-matter resources

JUDGMENT call (a). Verified against the real trestle models via `uv run`:

- `Observation.relevant_evidence` is a `list[RelevantEvidence]`.
- `RelevantEvidence` fields: `href`, `description`, `props`, `links`, `remarks`.

Each walkthrough is already serialized as one `Observation` (slice 030). Mapping
its attachments onto that **same observation's `relevant_evidence[]`** keeps the
captured artifact co-located with the walkthrough it evidences — which is exactly
the §8.3 promise ("walkthrough = narrative PLUS attachments") and the §8.5
promise ("walkthrough observation evidence"). Each attachment becomes one
`RelevantEvidence`:

- `href` = the object-storage URI (`storage_key`, the slice-036 key format) —
  the reference, **not the bytes** (AC-5 / P0-494-2).
- `description` = the filename + content-type, human-readable.
- `props` = `attachment-id`, `content-hash` (sha256), `content-type`,
  `annotation-ref` (AC-4).

**Rejected alternative — back-matter `Resource` + `links`:** back-matter is the
OSCAL home for _document-level_ shared resources referenced from many places.
A walkthrough attachment is evidence _for one specific observation_, so it
belongs on that observation. Back-matter would scatter the walkthrough's
evidence away from the walkthrough and force a two-hop deref (observation →
link → back-matter resource) for no benefit. `relevant_evidence` round-trips
cleanly through trestle (proven by the bridge round-trip test).

**Confidence: HIGH.** `relevant-evidence` is the OSCAL-native, single-hop,
spec-intended home; trestle accepts it; it matches the canvas language verbatim.

### D3 — Per-walkthrough attachment-reference cap = 50, with an honest overflow note

JUDGMENT call (b) + threat-model D (DoS via a walkthrough with very many
attachments bloating the AR). Cap the `relevant_evidence` entries per walkthrough
at **50**. When a walkthrough has more, the AR carries the first 50 (ordered by
`uploaded_at ASC, id ASC` — stable, matches `ListWalkthroughAttachments`) plus a
final synthetic `RelevantEvidence` whose `description` is the honest overflow
note: _"N attachments total; 50 shown. See the walkthrough record for the full
set."_ The cap is applied **Go-side** (in the aggregate read) so the bound is
enforced before the proto crosses to the bridge.

**Why 50:** a walkthrough is a narrated control demo; tens of screen captures +
a transcript is realistic, hundreds is not. 50 keeps the AR small while never
silently dropping evidence — the overflow note tells the auditor the full set
exists and where. The cap is a named constant
(`maxAttachmentRefsPerWalkthrough`) flagged for revisit.

**Confidence: MEDIUM.** The number is a judgment; the _mechanism_ (cap +
explicit overflow note, never silent truncation) is the load-bearing part and is
high-confidence.

### D4 — Proto extension: add `WalkthroughAttachment` message + `repeated` field on `Walkthrough`

The `Walkthrough` proto message had no attachment field. Added a
`WalkthroughAttachment` message (`id`, `filename`, `content_hash`,
`content_type`, `annotation_ref`, `storage_uri`) and a
`repeated WalkthroughAttachment attachments` field on `Walkthrough`. This is an
**additive, backward-compatible** proto change (new field numbers, no
renumbering) — P0-494-5 (AP/SSP/POA&M serializers untouched) and P0-494-6 (cosign
path untouched) both hold; only the AR observation enrichment changes.

**Confidence: HIGH.** Additive proto fields are the standard wire-evolution
move; the bridge ignores unknown fields and the new ones are AR-only.

---

## Revisit-once-in-use

- **D3 cap (50):** revisit if real walkthroughs routinely exceed it; the
  overflow note makes the cap safe to tune later without a schema change.
- **D1 most-recent-sample selection:** if the product later supports _named_
  audit samples (e.g., the auditor explicitly picks which draw is authoritative),
  the "most-recent" heuristic becomes "the selected sample." That is a
  product-surface slice, not a correctness change here.
- **Attachment `href` form:** currently the raw `storage_key`. If/when a signed
  presigned-URL surface lands (slice-036 follow-on), the AR `href` may graduate
  to that. The hash is the integrity anchor either way.

---

## Confidence summary

| Decision                               | Confidence                         |
| -------------------------------------- | ---------------------------------- |
| D1 read persisted draw (not recompute) | HIGH                               |
| D2 `relevant-evidence` mapping         | HIGH                               |
| D3 cap = 50 + overflow note            | MEDIUM (number) / HIGH (mechanism) |
| D4 additive proto fields               | HIGH                               |

---

## Detection-tier classification

- `detection_tier_actual`: `integration` — the load-bearing correctness points
  (frozen-population draw AC-7, reproducibility AC-9, tenant isolation AC-8) are
  caught by the new `//go:build integration` tests running through the real DB
  read path with the capturing fake bridge (so they execute on every CI run, not
  only when the Python bridge is present — the slice-493 D-test pattern).
- `detection_tier_target`: `integration` — a post-freeze leak or a cross-tenant
  reference is precisely an integration-tier concern (real RLS + real frozen
  horizon). Had AC-7 not existed and the bug shipped, it would be the canonical
  `target=integration, actual=production` invariant-#10 gap the slice notes name.

No defect surfaced during the build that escaped its target tier.
