# 651 — Map THR-05 / THR-06 / THR-07 into framework crosswalks (insider-threat + VDP)

**Cluster:** Catalog / UCF
**Estimate:** S (0.5d)
**Type:** JUDGMENT (crosswalk strength selection)
**Status:** `done` — THR-07 mapped; THR-05 / THR-06 gap confirmed and recorded.
The "blocked" premise held for insider threat but NOT for coordinated
disclosure: NIST CSF 2.0 `ID.RA-08` ("Processes for receiving, analyzing, and
responding to vulnerability disclosures are established") was already in the
bundled crosswalk — slice 514 added it, anchored to `VPM-04` alone, and slice
646's sweep looked at `ID.RA-01` instead. Slice 651 authored
`ID.RA-08 → THR-07 equal/0.9` and recorded the full per-framework search that
confirms no bundled framework carries a dedicated insider-threat requirement.
See `docs/audit-log/651-thr-insider-vdp-crosswalk-decisions.md`.

## Narrative

Slice 646 authored the finer SCF Threat-Management (THR) crosswalk edges,
mapping SOC 2 / ISO 27001 / NIST CSF requirements onto `THR-02` (Indicators of
Exposure), `THR-03` (Threat Intelligence Feeds), `THR-04` (Threat Hunting),
`THR-09` (Threat Catalog), and `THR-10` (Threat Analysis). It deliberately left
THREE finer THR controls with NO framework crosswalk edges:

- `THR-05` — Insider Threat Program
- `THR-06` — Insider Threat Awareness
- `THR-07` — Vulnerability Disclosure Program (VDP)

The reason (slice 646 D2): none of the five bundled framework crosswalks
(`soc2-tsc-2017`, `iso27001-2022`, `nist-csf-2.0`, `pci-dss-4.0`,
`hipaa-security-rule`) carries a DEDICATED insider-threat or
coordinated-disclosure requirement. The nearest candidates are general
security-awareness controls (which are NOT insider-threat awareness) and internal
vulnerability-management controls (which are NOT external coordinated-disclosure
intake). Slice 646 chose to author no edge rather than over-state those
relationships — the speculative-edge anti-pattern the parent slice forbade.

This slice authors the THR-05/06/07 edges once a bundled framework gains a
requirement with a genuine STRM relationship to them.

## Acceptance criteria (sketch — refine at pickup)

- [ ] When a bundled framework carries a dedicated insider-threat requirement
      (e.g. NIST 800-53 PM-12 "Insider Threat Program", AT-2(2) insider-threat
      awareness) or a coordinated-disclosure / VDP requirement (e.g. NIST 800-53
      RA-5(11), or ISO/IEC 29147), author the requirement → THR-05/06/07 edge
      with a justified STRM relationship + strength (invariant #7).
- [ ] No requirement → requirement edges (the b227 guard stays green).
- [ ] Do NOT scaffold a new framework crosswalk file; only add edges to a
      framework crosswalk file that already exists.
- [ ] An integration test asserts each new edge resolves through a real
      `fw_to_scf_edges` row; the existing crosswalk-import + schemaregistry
      drift/bijection suites stay green.
- [ ] Decisions log records the per-edge strength rationale.

## Outcome

- **THR-07 — MAPPED.** `nist_csf:2.0:ID.RA-08 → THR-07`, `equal` / `0.9`,
  additive alongside the existing `VPM-04 subset_of/0.75` remediation-side edge.
- **THR-05 / THR-06 — still unmapped, now with recorded evidence.** All 340
  requirements across the five bundled crosswalks were swept for insider-threat
  vocabulary; every hit (ISO A.5.3/A.6.1/A.6.4, CSF GV.RR-04/DE.CM-03/PR.AT-01,
  PCI 12.6.1, HIPAA 164.308(a)(3)/(a)(5), SOC 2 CC1.x) is a personnel-security,
  general-awareness, or monitoring control — none is an insider-threat program
  or insider-indicator training requirement. No edge authored; forcing one would
  fabricate coverage. `TestTHRInsiderAnchors_RemainUnmapped` guards the gap.

## Dependencies

- **#646** (finer THR crosswalk pass) — authored THR-02/03/04/09/10 edges and
  filed this gap.
- THR-05 / THR-06 remain waiting on a bundled framework with a dedicated
  insider-threat requirement (NIST 800-53 PM-12 / AT-2(2), or the full SCF
  catalog import). Bundling one is separate work with licensing implications.
