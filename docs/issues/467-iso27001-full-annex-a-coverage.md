# 467 — ISO 27001:2022 full Annex A coverage completion

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `ready`
**Parent:** #438 (ISO 27001:2022 crosswalk loader — curated subset)

## Narrative

Slice 438 shipped the framework-agnostic crosswalk importer and a **curated
36-control subset** of ISO/IEC 27001:2022 Annex A — enough to prove UCF
invariant #1 (one SCF anchor satisfies SOC 2 + ISO at once) and seed the
catalog. The slice's scope discipline explicitly deferred full Annex A
coverage to a follow-on; this is that follow-on.

Complete the remaining ~57 Annex A controls (Annex A has 93 total; slice 438
shipped 36) in `data/crosswalks/iso27001-2022.yaml`, mapping each to its SCF
anchor with an STRM-typed edge. This is pure data + decisions-log work on the
already-generalized loader — no code change. The slice 438 decisions log
("Revisit once in use") lists the specific gaps to close:

- The remaining A.5 organizational sub-controls (A.5.3–A.5.6, A.5.11, A.5.13,
  A.5.14, A.5.16, A.5.20, A.5.21, A.5.25, A.5.27, A.5.28, A.5.32–A.5.34,
  A.5.36, A.5.37).
- The remaining A.6 people controls (A.6.1, A.6.2, A.6.4, A.6.6, A.6.7, A.6.8).
- The remaining A.7 physical controls (all except A.7.2 + A.7.10).
- The remaining A.8 technological controls (A.8.1, A.8.3, A.8.4, A.8.6,
  A.8.10, A.8.12, A.8.14, A.8.17–A.8.19, A.8.21–A.8.23, A.8.26, A.8.28–A.8.34).

Also re-map the two slice-438 low-confidence stopgaps (`A.5.7` threat
intelligence → `MON-08`; `A.8.11` data masking → `DCH-01`) once the full SCF
catalog with threat-intel + data-masking families is importable, and split
`A.8.24` into the encryption-in-transit + key-management edge pair.

## Acceptance criteria

- [ ] **AC-1.** All 93 ISO 27001:2022 Annex A controls present in
      `data/crosswalks/iso27001-2022.yaml` with STRM-typed edges to SCF anchors
      (requirement → SCF anchor only — invariant #7).
- [ ] **AC-2.** Decisions log updated with the per-control mapping rationale +
      confidence for the newly-added controls.
- [ ] **AC-3.** Integration test asserts the full count imports cleanly and
      does not regress SOC 2 or the slice-438 subset.
- [ ] **AC-4.** Changelog entry.

## Anti-criteria (P0)

- **P0-467-1.** No requirement → requirement edge (invariant #7).
- **P0-467-2.** No verbatim copyrighted ISO standard text — identifiers +
  titles + original descriptions only (slice 438 licensing posture).
- **P0-467-3.** No bundled pre-built SCF data (slice 438 P0-438-7).

## Dependencies

- **#438** (ISO 27001:2022 crosswalk loader) — the generalized loader + the
  curated subset this slice extends.
