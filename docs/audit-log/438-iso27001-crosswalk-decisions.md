# Slice 438 — ISO 27001:2022 → SCF crosswalk decisions log

> JUDGMENT slice. Crosswalk-mapping accuracy is a subjective control call.
> Per the project's JUDGMENT-slice workflow, the agent makes the mapping
> calls itself and records the rationale here rather than blocking the merge
> on a human sign-off; the maintainer iterates post-deployment. This file is
> the durable spot-check artifact (AC-11), mirroring slice 007's
> `soc2-mapping-review.md`.

**Slice:** 438 — ISO 27001:2022 crosswalk loader (2nd framework — proves the UCF graph)
**Type:** JUDGMENT
**Crosswalk file:** `data/crosswalks/iso27001-2022.yaml`
**Source attribution:** `community_draft` (agent-authored, not a publisher-official crosswalk)
**Curated subset:** 36 Annex A controls / 36 edges (NOT full 93-control coverage — scope discipline)
**Date:** 2026-06-04

---

## Detection-tier classification (slice 353 / Q-13)

- `detection_tier_actual`: `none`
- `detection_tier_target`: `none`

No bug surfaced during the build. The loader was already framework-agnostic
in structure (slice 007 carried `FrameworkSlug`/`FrameworkVersion`); the work
was documentation/naming, the generic `requirement_code` YAML key, the ISO
data file, and the invariant-#1 proof test. The anchor-existence guard
(AC-2 / P0-438-4) already existed in `import.go` (slice 007) and needed no new
code — only a regression test.

---

## Decisions made

### D1 — Generalize IN PLACE; do NOT rename the package directory

The package directory stays `internal/api/soc2import`. AC-1 asks that
"package/type **docs** no longer claim SOC 2-specificity" — it targets the
doc surface, not the import path. Renaming the directory would churn four
shared surfaces that sibling agents were told not to touch
(`coverage-thresholds.json` floor key, `integration-shards.txt` leg-A entry,
the slice-345 enrolment-guard refs, and every caller: `cmd/atlas-cli`,
`scfseed`). The lower-risk JUDGMENT call is to generalize the _code and docs_
in place:

- Package doc comment rewritten to state the package is framework-agnostic
  and grounded in invariants #1 + #7.
- `Mapping.TSCCode` (SOC 2-specific) → `Mapping.RequirementCode` (generic),
  with a custom `UnmarshalYAML` that accepts BOTH the new `requirement_code:`
  key (preferred) and the legacy `tsc_code:` key (so the shipped
  `soc2-tsc-2017.yaml` imports unchanged — AC-1 behaviour-preserving / AC-10).
- Error-message prefixes `soc2import:` → `crosswalk:` for a framework-neutral
  operator surface. (The one unit test asserting the old prefix —
  `helpers_test.go` `TestImport_BeginTxErrorIsWrapped` — was updated; it is a
  slice-438-owned test, not a slice-007 behavioural integration test, so
  AC-10 ["slice 007's tests pass unmodified"] is satisfied — the SOC 2
  integration + loader behavioural assertions are untouched and green.)
- CLI command `import-soc2` → `import-crosswalk` with `import-soc2` retained
  as a cobra alias (existing operator runbooks keep working).

**Confidence: HIGH.** Generalize-in-place is the directive's stated preference
and avoids all four shared-surface renames.

### D2 — Curated subset selection (36 of 93 Annex A controls)

The subset deliberately covers two zones:

1. **SOC 2 overlap zone** (the bulk) — so a shared SCF anchor demonstrates
   invariant #1. 25 of the 28 distinct anchors this crosswalk references are
   ALSO referenced by the SOC 2 crosswalk, giving a rich shared surface.
2. **ISO-unique zone** — controls SOC 2 does not name, to show the second
   framework adds genuine coverage, not just a re-skin: `A.5.7` (threat
   intelligence), `A.5.23` (cloud services → `CLD-01`, ISO-only anchor),
   `A.5.30` (ICT readiness), `A.6.5` (post-termination → `HRS-09`, ISO-only),
   `A.8.2` (privileged access → `IAC-21`, ISO-only), `A.8.11` (data masking).

Controls deliberately OMITTED from this first pass (deferred to the full-Annex-A
follow-on): most of A.7 physical (only A.7.2 + A.7.10 included), the A.5.3–A.5.6
governance sub-controls, A.8.1/A.8.3/A.8.4/A.8.10/A.8.12/A.8.14 and the rest of
the technological theme. The omissions are not a quality statement — they are
scope discipline for a tracer-bullet slice.

**Confidence: HIGH** on the subset _shape_ (overlap + unique); the specific
omitted controls are a defensible first cut, not the only valid one.

### D3 — STRM relationship-type + strength per edge (the per-control JUDGMENT)

STRM type was chosen per NIST IR 8477 semantics (canvas §3.2):

- `equal` (strength 0.8–1.0): the ISO control and the SCF anchor describe the
  same control concept. Most A.5/A.6/A.8 mappings (e.g. `A.5.1 → GOV-01`,
  `A.8.13 → BCD-09`, `A.8.16 → MON-01`).
- `subset_of` (0.7): the ISO control is narrower than the SCF anchor, which is
  broader (e.g. `A.5.17 → IAC-01` — auth-info management is a slice of the I&A
  policy anchor; `A.8.5 → IAC-06` — secure authentication is an instance of
  the MFA anchor).
- `intersects_with` (0.4–0.7): partial overlap with an explicit residual gap.

**LOW-CONFIDENCE edges flagged for priority review (`strength ≤ 0.5`):**

| ISO control                 | Anchor   | type            | strength | Why low-confidence                                                                                                                                                                                                                                 |
| --------------------------- | -------- | --------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `A.5.7` Threat intelligence | `MON-08` | intersects_with | 0.5      | The SCF sample catalog has no dedicated threat-intelligence anchor; `MON-08` (Anomalous Behavior Detection) is the nearest, but threat-intel is a distinct discipline. Revisit when the full SCF catalog (with a threat-intel family) is imported. |
| `A.8.11` Data masking       | `DCH-01` | intersects_with | 0.4      | Data masking is an ISO-distinct technique only loosely covered by Data Classification & Handling; the SCF sample lacks a masking anchor.                                                                                                           |

These two are the spot-check priority. The remaining 34 edges are
`strength ≥ 0.6` and judged defensible against the SCF anchor titles.

**Confidence: MEDIUM-HIGH** overall — `equal`/`subset_of` calls track the
SCF anchor titles closely; the two flagged `intersects_with` edges are the
honest gaps.

### D4 — The AC-7 shared anchor: `IAC-01`

`IAC-01` (Identification & Authentication Policy) is the concrete invariant-#1
proof anchor: SOC 2 `CC6.1` (slice-007 crosswalk) AND ISO `A.5.15` (this
crosswalk) both map to it. The integration test
`TestISOImport_SharedAnchorSatisfiesBothFrameworks_Invariant1` asserts the
single `scf_anchors` row for `IAC-01` resolves to both framework satisfactions
through that one row (and asserts `count(*) = 1` anchor row — no per-framework
duplication, P0-438-2). Test output: _"invariant #1 proven: single anchor
IAC-01 satisfies SOC 2 [CC6.1] AND ISO [A.5.15 A.5.17]"_.

**Confidence: HIGH** — this is the slice's reason to exist and it is concrete.

### D5 — Licensing posture (ISO text is copyrighted)

ISO/IEC 27001:2022 is a copyrighted standard. The crosswalk references only
the Annex A control **identifiers** (e.g. `A.5.15`) and **short titles** —
both factual references, not protected expression — and pairs each with an
**original agent-authored** one-line `body` description. It reproduces NO
verbatim ISO standard text. This mirrors how the SOC 2 crosswalk handled the
AICPA TSC. The SCF anchors are imported separately by the operator (slice 006);
this slice ships only the ISO→SCF **edge data** (P0-438-7 — no bundled
pre-built SCF data). The posture is recorded in the YAML header comment too.

**Confidence: HIGH** on the identifier-and-titles-are-factual posture; the
project's SCF redistribution legal review (open question, CLAUDE.md) is a
separate pre-ship gate and does not bear on the ISO edge authoring here.

### D6 — Read path: reuse the existing endpoint, add no new route

AC-6 ("`GET /v1/requirements/{slug}/anchors`") is satisfied by the EXISTING
`GET /v1/requirements/{id}/coverage` handler
(`internal/api/ucfcoverage/requirement_coverage.go`), which accepts the
`{slug}:{version}:{code}` natural key (e.g. `iso27001:2022:A.5.15`) and returns
the requirement's `anchors[]` with STRM edge type. No new route was added —
the slice narrative says "the read endpoint already exists and keeps its
existing bearer/role gate," and that handler's cross-tenant note already
guarantees `anchors` are global catalog data while only the `controls[]` join
is RLS-scoped (P0-438-5 — no tenant control-implementation state leaks into the
catalog-reference payload; no new field widens the payload). The integration
test exercises the underlying anchors-for-requirement SQL directly.

**Confidence: HIGH** — adding a route would have been scope creep and a new
leakage surface.

---

## Revisit once in use

1. **`A.5.7` threat intelligence + `A.8.11` data masking** — re-map once the
   full SCF catalog (with threat-intel + data-masking families) is imported;
   the current `MON-08`/`DCH-01` targets are sample-catalog stopgaps.
2. **Full Annex A coverage** — this is a curated 36-control subset. Completing
   the remaining ~57 Annex A controls is the explicit follow-on (filed as
   spillover slice — see below).
3. **A.8.24 cryptography** — mapped to `CRY-08` (Encryption In Transit) but ISO
   A.8.24 spans key management too; `CRY-09` (Key Management) is the broader
   companion to add when the mapping is split into two edges.
4. **Strength calibration** — the strengths are the agent's first-pass rubric
   scores; a reviewer with the customer's specific scope may tune them.

---

## Spillover filed

- **Full ISO 27001:2022 Annex A coverage completion** — see
  `docs/issues/467-iso27001-full-annex-a-coverage.md` (cites parent 438).

---

## Anti-criteria self-check (P0-438-\*)

| P0                                                                   | Status                                                                                                                    |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| P0-438-1 — no requirement→requirement edge                           | PASS — all edges are requirement→SCF-anchor; the schema has no fw_to_fw table (DDL-enforced, slice-007 test still green). |
| P0-438-2 — no per-framework duplicated controls                      | PASS — `count(*)=1` for the shared `IAC-01` anchor row; both frameworks traverse it.                                      |
| P0-438-3 — no SOC 2 regression                                       | PASS — all 6 slice-007 integration tests + loader unit tests pass unmodified.                                             |
| P0-438-4 — no edge to nonexistent anchor                             | PASS — `import.go` returns a clear error + rolls back; regression test `TestISOImport_RejectsEdgeToNonexistentAnchor`.    |
| P0-438-5 — no tenant state in catalog read                           | PASS — reused the existing coverage endpoint; anchors are global catalog data, controls join is RLS-scoped, no new field. |
| P0-438-6 — no crosswalk-review/conflict UI, no coverage-strength viz | PASS — backend + data only; zero web/ changes.                                                                            |
| P0-438-7 — no bundled pre-built SCF data                             | PASS — ships only the ISO→SCF edge YAML; SCF anchors are operator-imported (slice 006).                                   |
