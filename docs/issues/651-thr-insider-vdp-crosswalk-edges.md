# 651 — Map THR-05 / THR-06 / THR-07 into framework crosswalks (insider-threat + VDP)

**Cluster:** Catalog / UCF
**Estimate:** S (0.5d)
**Type:** JUDGMENT (crosswalk strength selection)
**Status:** `blocked` (waiting on a bundled framework that carries a dedicated
insider-threat or coordinated-disclosure requirement — see Dependencies)

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

## Dependencies

- **#646** (finer THR crosswalk pass) — authored THR-02/03/04/09/10 edges and
  filed this gap.
- A bundled framework with a dedicated insider-threat / VDP requirement (e.g.
  the full SCF catalog import, or a NIST 800-53 crosswalk) — none of the current
  five carries one, which is why this slice is `blocked` rather than `ready`.
