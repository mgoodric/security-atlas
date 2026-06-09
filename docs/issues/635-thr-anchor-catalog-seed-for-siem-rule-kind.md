# 635 — Seed the THR (threat-detection) SCF anchor for the SIEM-rule kind

**Cluster:** Catalog / UCF
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (anchor selection / catalog mapping)
**Status:** `ready` (follow-on of #533 — Datadog Cloud-SIEM rule evidence — merged first)

## Narrative

Slice 533 shipped the `datadog.siem_rule.v1` evidence kind with
`x-default-scf-anchors = [MON-01, THR-01]`, as that slice directed. `MON-01`
resolves against the bundled SCF sample catalog
(`migrations/fixtures/scf-sample.json` / `internal/api/scfseed`), but **`THR-01`
does not yet exist in the seeded catalog** — the sample catalog carries the MON
domain (incl. `MON-08` "Anomalous Behavior Detection") but no THR-domain anchor.

`x-default-scf-anchors` is advisory connector-side default metadata (slice-488 D2;
the evidence_kind drift guard validates `x-evidence-kind` bijection, not the
anchor list against the catalog), so this is NOT a broken build — the advisory
anchor simply does not resolve to a catalog row today. See slice 533 decisions-log
D2 for the full caveat.

This slice closes that gap: either (a) seed the `THR-01` ("Threat Intelligence /
Threat-Detection Program") anchor (+ STRM crosswalk rows) into the catalog so the
advisory anchor resolves, or (b) if the full SCF import (which carries the real
THR domain) lands first and uses a different THR identifier, remap the
`datadog.siem_rule.v1` schema's advisory anchor to match. The JUDGMENT is the
correct canonical SCF anchor for "detection rules are configured" once the real
catalog is present (THR-01 vs MON-08 vs a more specific THR sub-control).

## Acceptance criteria (sketch — refine at pickup)

- [ ] `THR-01` (or the agreed detection anchor) resolves against the seeded SCF
      catalog, OR the `datadog.siem_rule.v1` advisory anchor is remapped to a
      catalog-resolving anchor.
- [ ] The anchor choice is recorded (decisions log) with the STRM crosswalk to
      SOC 2 CC7.2/CC7.3 + ISO A.12.
- [ ] No change to the evidence-kind shape or the drift/bijection guard.

## Dependencies

- **#533** (Datadog SIEM-rule evidence) — introduced the advisory `THR-01`
  reference.
- Coordinates with the full SCF catalog import (real THR domain).
