# 646 — Map the finer SCF THR controls into the framework crosswalks

**Cluster:** Catalog / UCF
**Estimate:** S (0.5d)
**Type:** JUDGMENT (crosswalk strength selection)
**Status:** `ready` (follow-on of #641 — full THR domain import — merged first)

## Narrative

Slice 641 imported the full SCF Threat-Management (THR) domain into the bundled
sample catalog (THR-01..THR-10) and re-evaluated the slice-635 THR-01 crosswalk
edges (decision kept them as-is). The eight new controls (THR-02 Indicators of
Exposure, THR-03 Threat Intelligence Feeds, THR-04 Threat Hunting, THR-05
Insider Threat Program, THR-06 Insider Threat Awareness, THR-07 Vulnerability
Disclosure Program, THR-09 Threat Catalog, THR-10 Threat Analysis) now have
`scf_anchors` rows but **no framework crosswalk edges** — they resolve in the
catalog but no SOC 2 / ISO 27001 / NIST CSF / PCI DSS / HIPAA requirement points
at them yet.

Slice 641 D3 deliberately scoped this OUT: that slice's mandate was to re-evaluate
the _existing_ slice-635 THR-01 edges, not author new ones for the freshly-imported
controls. This slice does the finer-grained crosswalk pass.

Candidate edges to evaluate (requirement → SCF anchor, STRM-typed, invariant #7):

- SOC 2 `CC7.2` → THR-04 (Threat Hunting) — the operational hunting activity that
  detects malicious-act anomalies evading existing controls.
- ISO 27001 `A.8.16` (Monitoring activities) → THR-04.
- ISO 27001 `A.5.7` (Threat intelligence) → THR-03 (Threat Intelligence Feeds) as
  an additional subset edge alongside the existing THR-01 equal.
- Vendor / third-party requirements → THR-07 (Vulnerability Disclosure Program).
- Risk-assessment requirements → THR-10 (Threat Analysis) / THR-09 (Threat Catalog).
- Insider-threat / personnel-security requirements → THR-05 / THR-06.

## Acceptance criteria (sketch — refine at pickup)

- [ ] Each finer THR control that has a genuine STRM relationship to a framework
      requirement gains a requirement → SCF anchor edge with a justified strength.
- [ ] No requirement → requirement edges (the b227 guard stays green).
- [ ] The schemaregistry drift/bijection guard and all crosswalk-import integration
      suites stay green; the slice-641 THR-domain resolution tests stay green.
- [ ] Decisions log records the per-edge strength rationale.

## Dependencies

- **#641** (full SCF THR domain import) — seeded the anchors this slice maps.
- **#635** (THR-01 detection anchor seed) — the existing THR-01 edges.
