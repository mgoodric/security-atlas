# 641 — Import the full SCF Threat-Management (THR) domain + reconcile the SIEM-rule anchor

**Cluster:** Catalog / UCF
**Estimate:** S–M (0.5–1.5d)
**Type:** JUDGMENT (anchor granularity / catalog coverage)
**Status:** `ready` (follow-on of #635 — THR-01 detection anchor seed — merged first)

## Narrative

Slice 635 seeded a single domain-head anchor `THR-01` ("Threat Intelligence
Program") into the bundled SCF sample catalog so the `datadog.siem_rule.v1`
advisory detection anchor resolves, and added its STRM crosswalk edges to
SOC 2 CC7.2/CC7.3 and ISO 27001 A.5.7/A.8.16 (see slice 635 decisions-log D2/D3).
That was deliberately the curated-subset grain — one representative anchor per
domain, matching how the sample catalog seeds MON-01, GOV-01, etc.

The real SCF Threat-Management domain carries more than one control (threat-intel
feeds, indicators-of-compromise handling, threat hunting, detection-rule
configuration as its own sub-control, etc.). When the full SCF import lands (the
real catalog rather than the 54-anchor sample subset), this slice:

1. Imports the full SCF THR domain with the verbatim SCF control text/identifiers.
2. Reconciles `THR-01`'s placeholder description (slice 635 used the catalog's own
   one-line paraphrase style) against the verbatim SCF THR-01 control text.
3. **JUDGMENT:** decides whether `datadog.siem_rule.v1`'s advisory detection
   anchor should remain `THR-01` (domain head) or move to a finer THR sub-control
   that more precisely names "detection rules are configured" — and, if it moves,
   updates the schema's advisory `x-default-scf-anchors`, the connector's
   `--siem-control` default, the README, and the connector tests in lockstep
   (the six bind-sites slice 635 D1 enumerated).
4. Re-evaluates the slice-635 crosswalk strengths (esp. SOC 2 CC7.2 THR-01
   `intersects_with/0.7` — a finer detection-rule sub-control may warrant `equal`).

## Acceptance criteria (sketch — refine at pickup)

- [ ] Full SCF THR domain present in the catalog with verbatim SCF identifiers/text.
- [ ] THR-01 description reconciled against verbatim SCF text (or the SIEM-rule
      anchor moved to the agreed finer sub-control, all six bind-sites in lockstep).
- [ ] Crosswalk strengths re-evaluated; the schemaregistry drift/bijection guard
      and the slice-635 resolution test stay green.
- [ ] No change to the `datadog.siem_rule.v1` evidence-kind shape.

## Dependencies

- **#635** (THR-01 detection anchor seed) — seeded the domain-head placeholder.
- **#533** (Datadog SIEM-rule evidence) — owns the advisory anchor + its 6 bind-sites.
- Coordinates with the full SCF catalog import (real THR domain).
