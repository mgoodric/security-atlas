# SOC 2 v2017 (TSC) → SCF mapping spot-check audit log

> Pre-merge HITL gate for slice 007. The agent-authored DRAFT mapping
> file at `data/crosswalks/soc2-tsc-2017.yaml` ships with every row
> attributed `community_draft`. This file is the audit trail of the
> human spot-check that converts those drafts into the slice's
> merge-ready artifact. PR #007 is held open at `in-review` until
> 20 mappings are reviewed and the reviewer signs below.

## Review status

**Status:** PENDING — orchestrator + user spot-check
**Reviewer:** (to be filled at HITL gate)
**Review date:** (to be filled at HITL gate)
**Crosswalk file:** `data/crosswalks/soc2-tsc-2017.yaml`
**Crosswalk source attribution:** `community_draft` (agent-authored, not SCF-published)
**Total drafted mappings:** 56
**Low-confidence flagged (`strength ≤ 0.5`):** 9 — review first

## Review priority order

The 9 low-confidence (`strength ≤ 0.5`) mappings cluster around two
families where SCF coverage is intentionally narrow and the agent's
mapping is least defensible:

1. **CC1.x (Control Environment)** — COSO governance principles that
   SCF only loosely encodes in GOV/HRS families:
   - `CC1.4 → HRS-04` `intersects_with` strength 0.5
   - `CC1.5 → HRS-01` `intersects_with` strength 0.5
   - `CC2.1 → GOV-01` `intersects_with` strength 0.5
2. **CC3.4 (Change Risk Assessment) supplemental**:
   - `CC3.4 → CFG-04` `intersects_with` strength 0.5
3. **A1.1 (Capacity)** — SCF has no direct capacity-management anchor:
   - `A1.1 → MON-01` `intersects_with` strength 0.5
4. **PI1.x (Processing Integrity)** — SCF treats PI primarily as a
   software-development concern; the AICPA TSC PI criteria are broader:
   - `PI1.1 → GOV-01` `intersects_with` strength 0.4
   - `PI1.2 → SEA-05` `intersects_with` strength 0.5
   - `PI1.3 → SEA-05` `intersects_with` strength 0.5
   - `PI1.4 → SEA-05` `intersects_with` strength 0.4
   - `PI1.5 → DCH-03` `intersects_with` strength 0.4

After resolving the low-confidence set, the reviewer should sample 11
additional mappings across CC6, CC7, CC8, CC9, A1, and C1 — 20 total —
and either:

- approve each as-is (update row in audit log + sign),
- adjust `relationship_type` / `strength` / `rationale` in the YAML
  before re-import,
- or reject and replace with `no_relationship` if no real overlap exists.

## High-confidence (`strength ≥ 0.9`) mappings — spot-check baseline

These should pass without modification if the agent's STRM judgment is
defensible; flagging any of these would surface a systemic problem
with the draft:

- `CC3.2 → RSK-04` equal 1.0 — "Risk Assessment" ↔ "Risk Assessment"
- `CC6.1 → IAC-01` subset_of 0.9 — IAC-01 is broader, fully covers
- `CC6.2 → IAC-07` equal 1.0 — "User Provisioning & Lifecycle"
- `CC6.4 → PES-04` equal 1.0 — Physical Access Control
- `CC6.5 → AST-09` equal 0.9 — Asset Disposal
- `CC6.8 → END-07` equal 1.0 — Malicious Code Protection
- `CC7.2 → MON-08` equal 1.0 — Anomalous Behavior Detection
- `CC7.4 → IRO-04` equal 1.0 — Incident Response Plan
- `CC8.1 → CHG-02` equal 1.0 — Change Control Process
- `CC9.2 → TPM-01` equal 1.0 — Third-Party Management
- `A1.3 → BCD-11` equal 1.0 — Backup Testing
- `C1.1 → DCH-01` equal 1.0 — Data Classification & Handling

## Per-mapping review log

(Reviewer: append one row per mapping reviewed. Format: TSC code →
SCF anchor | relationship_type/strength | approved | notes.)

| TSC | SCF | relationship/strength | approved? | reviewer notes |
| --- | --- | --------------------- | --------- | -------------- |

## Sign-off

(Reviewer: fill below when ≥20 mappings reviewed.)

- Reviewer name: …
- Reviewer role: …
- Review date: …
- Total mappings reviewed: …
- Mappings approved as-is: …
- Mappings revised before merge: …
- Mappings rejected (replaced with `no_relationship`): …
- Signature / commit SHA of merge: …
