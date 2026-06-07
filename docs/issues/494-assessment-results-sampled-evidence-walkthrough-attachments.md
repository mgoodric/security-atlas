# 494 — Assessment-Results export: wire the drawn sample evidence + walkthrough attachments

**Cluster:** audit (OSCAL)
**Estimate:** M (1-2d)
**Type:** JUDGMENT (which evidence-record fields belong in an AR observation, and how attachments map to OSCAL back-matter, are conformance calls)
**Status:** `ready`

## Narrative

Canvas §8.2 defines the OSCAL **Assessment Results** artifact as "**Sampled
evidence records** + auditor pass/fail/finding annotations," and §8.5 says the
auditor↔auditee comment thread is "exported to OSCAL `assessment-results`
`observation` annotations." Two pieces of that promise are unwired in the AR
exporter (`internal/oscal/aggregate.go`, `assessmentInput()`):

1. **The drawn sample evidence IDs are not exported.** The AR carries each
   sample _population_ (population id, control id, population size, frozen
   horizon) — but `SampledEvidenceIds: nil` with an explicit comment: "The
   sampled evidence ids are drawn by slice 026's Sample primitive; ... A future
   revision can join the drawn sample rows." So the AR tells an auditor "we drew
   N from a population of M" but **not which N records were drawn** — exactly the
   evidence §8.2 says the AR must carry. The deterministic sampler
   (`internal/audit/sample`, slice 026: `Sample(population, n, seed)`) produces a
   reproducible draw; that draw is simply not joined into the export.

2. **Walkthrough attachments are not exported.** §8.1 / §8.3 define a
   walkthrough as a narrative **plus attachments** (annotated screen captures +
   transcript, hashed and signed). The AR carries the walkthrough narrative +
   status + canonical hash, but **not the attachments** — the screen captures and
   transcripts that are the actual walkthrough evidence. `aggregate.go` has no
   attachment handling in the walkthrough loop, even though
   `internal/audit/walkthrough` stores attachment metadata + hashes.

Net effect: the AR is structurally a shell. An auditor importing the AR into
their own OSCAL tooling sees populations and walkthrough narratives but cannot
trace from an observation to the specific evidence record or the walkthrough's
captured artifact. For a tool whose v1 promise is "the auditor does their work
in our tool," an AR that omits the sampled records is the gap most likely to send
the auditor back to a spreadsheet of evidence IDs.

This slice **wires both into the AR**: (1) join the deterministic sample draw
(reuse `audit/sample.Sample` with the persisted seed) so each `SamplePopulation`
in the AR carries its `sampled_evidence_ids[]`; (2) carry walkthrough attachment
metadata (id, filename, content hash, content type, annotation reference) into
the AR as OSCAL `observation` evidence references / back-matter resources. The
attachment **bytes** are not embedded — the AR references them by hash + the
existing object-storage URI pattern (the signed bundle is the integrity layer).

**Scope discipline.** AR observation enrichment only. Does NOT change the AP, SSP,
or POA&M serializers. Does NOT embed attachment binary content in the AR JSON
(reference-by-hash only — keeps the AR small and the object store the source of
truth). Does NOT change the sampler determinism contract (slice 026) — it
**consumes** the existing draw. Does NOT add auditor pass/fail annotation
authoring (that is the finding/annotation surface; this slice carries what
already exists into the AR).

## Threat model (STRIDE)

The AR is a **signed, audit-binding artifact** drawn from frozen audit-period
state. The load-bearing concern is audit-period-freezing tamper-resistance
(invariant #10) and the integrity of the evidence references it now carries.

**S — Spoofing.** N/A — no new ingress; AR export is the existing authenticated,
tenant-scoped operation.

**T — Tampering (PRIMARY — invariant #10).** The whole point of audit-period
freezing is that the sample population (and now the drawn sample) cannot shift
under the auditor's feet. Risk: the AR's sampled IDs are computed at export time
from live data instead of the frozen horizon, so a record observed _after_
`frozen_at` sneaks into the sample.
**Mitigation:** the sample draw MUST run against the **frozen population** —
evidence with `observed_at ≤ frozen_at` (invariant #10, the slice 026/028
contract) — using the persisted seed, so the AR's `sampled_evidence_ids` are
deterministic and reproducible from the frozen state. An integration test
freezes a period, adds post-freeze evidence, and proves the post-freeze record
NEVER appears in the AR's sampled set. Walkthrough attachments carry their
stored content hash so a post-export swap of the underlying artifact is
detectable; the AR remains cosign-signed.

**R — Repudiation.** "Which exact records did the auditor sample, and can the
draw be reproduced?"
**Mitigation:** the AR now carries the drawn IDs + the population's frozen
horizon + (implicitly, via the population) the seed-driven draw — the draw is
reproducible by re-running `Sample(frozen_population, n, seed)`. This is the
reproducibility §8.3 names ("deterministic, reproducible").

**I — Information disclosure.** Sampled evidence IDs + attachment metadata are
confidential per tenant. The AR must not leak cross-tenant references.
**Mitigation:** the sample join + attachment read run under the audit period's
tenant `app.current_tenant`; an integration test proves Tenant A's sampled
evidence / attachments never appear in Tenant B's AR. Attachment **bytes** are
referenced (hash + URI), not embedded — no raw payload leaves through the AR
JSON, and the object-store URI is access-controlled independently.

**D — Denial of service.** A control with a huge frozen population, or a
walkthrough with very many attachments, could bloat the AR.
**Mitigation:** the AR carries the _drawn sample_ (bounded by `n`), not the full
population, so sample size is already capped; attachments are referenced by
metadata (bounded), not embedded. A per-export cap on attachment references with
an honest "N attachments; see the walkthrough" overflow note.

**E — Elevation of privilege.** N/A — no new authz; export remains gated to the
existing AR-export roles; the auditor's read-only time-windowed scope (§8.1) is
unchanged.

## Acceptance criteria

**Sampled evidence**

- [ ] **AC-1.** Each `SamplePopulation` in the AR carries `sampled_evidence_ids[]`
      produced by the deterministic sampler (`audit/sample.Sample`) using the
      population's persisted seed.
- [ ] **AC-2.** The draw runs against the **frozen population** (`observed_at ≤
frozen_at`, invariant #10) — never live data.
- [ ] **AC-3.** The `SampledEvidenceIds: nil` placeholder + its comment are
      removed.

**Walkthrough attachments**

- [ ] **AC-4.** Each `Walkthrough` in the AR carries its attachment references
      (id, filename, content hash, content type, annotation reference) mapped to
      OSCAL `observation` evidence references / back-matter resources.
- [ ] **AC-5.** Attachment **bytes are not embedded** — referenced by hash + the
      existing object-storage URI only.

**Tests**

- [ ] **AC-6.** Integration test (`//go:build integration`): an AR exported for a
      frozen period carries the expected drawn sample IDs (reproducible via the
      sampler) and walkthrough attachment references (via the bridge → OSCAL
      JSON).
- [ ] **AC-7.** **Freeze-integrity integration test (invariant #10):** freeze a
      period, add post-`frozen_at` evidence, export the AR — the post-freeze
      record NEVER appears in `sampled_evidence_ids`.
- [ ] **AC-8.** Tenant-isolation integration test: Tenant A's sampled evidence /
      attachment references never appear in Tenant B's AR (threat-model I).
- [ ] **AC-9.** Reproducibility test: re-running the sampler with the persisted
      seed against the frozen population yields the same drawn set the AR carries
      (threat-model R).

**Docs**

- [ ] **AC-10.** Auditor docs note the AR now carries the drawn sample +
      attachment references; a changelog entry for the slice.

## Constitutional invariants honored

- **#10 — Audit-period freezing.** The sample draw is over the frozen population;
  proven by AC-7. The AR is a faithful, reproducible snapshot.
- **#8 — OSCAL is the wire format.** The AR now carries the "sampled evidence
  records" §8.2 specifies and the walkthrough observation evidence §8.3 / §8.5
  promise.
- **#6 — Tenant isolation via RLS.** Sample + attachment reads are tenant-scoped;
  proven by AC-8.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.2 (AR = sampled evidence records),
  §8.3 (deterministic reproducible Sample + Walkthrough attachments), §8.4
  (audit-period freezing), §8.5 (notes → AR observation annotations).
- `CLAUDE.md` invariant #10 (audit-period freezing — frozen-population draw).

## Dependencies

- **#026** (deterministic Sample primitive) — `merged`. The draw this slice
  joins into the AR.
- **#028** (audit-period freezing — frozen evidence horizon) — `merged`. The
  frozen population the draw runs against.
- **#030** (OSCAL AR export) — `merged`. The exporter this slice completes.
- **walkthrough attachments** (`internal/audit/walkthrough`) — `merged`. The
  attachment metadata this slice carries into the AR.

## Anti-criteria (P0 — block merge)

- **P0-494-1.** Does NOT sample from live data — the draw is over the frozen
  population (invariant #10, AC-2/AC-7).
- **P0-494-2.** Does NOT embed attachment bytes in the AR JSON — reference by
  hash + URI only (AC-5).
- **P0-494-3.** Does NOT leak cross-tenant sampled evidence / attachments
  (threat-model I, AC-8).
- **P0-494-4.** Does NOT change the sampler determinism contract — consumes the
  existing draw (slice 026).
- **P0-494-5.** Does NOT change the AP/SSP/POA&M serializers — AR only.
- **P0-494-6.** Does NOT alter the cosign signing path.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; the freeze-integrity +
reproducibility tests are load-bearing) · `database-designer` (the sample-draw
join + attachment-reference read) · `security-review` (audit-binding artifact +
freeze tamper-resistance + tenant scope) · `simplify`.

## Notes for the implementing agent

- **JUDGMENT calls you own:** how walkthrough attachments map onto OSCAL
  back-matter `resource` vs `observation` `relevant-evidence` (OSCAL-conformance
  call — pick the structure compliance-trestle round-trips cleanly and record
  it); the per-walkthrough attachment-reference cap; whether to persist the drawn
  sample at freeze time or compute it at export time from the persisted seed
  (compute-from-seed keeps the freeze cheap and is the slice-028 spirit — but
  confirm the seed is persisted on the population row). Record in the decisions
  log with confidence.
- Reuse `audit/sample.Sample` verbatim — do NOT reimplement the draw.
- Detection-tier: a post-freeze leak caught by AC-7 is `target=integration,
actual=integration`; if it slipped to production it would be the canonical
  `target=integration, actual=production` invariant-#10 gap.
