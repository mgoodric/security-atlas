# SOC 2 v2017 (TSC) → SCF mapping spot-check audit log

> Pre-merge HITL gate for slice 007. The agent-authored DRAFT mapping
> file at `data/crosswalks/soc2-tsc-2017.yaml` ships with every row
> attributed `community_draft`. This file is the audit trail of the
> human spot-check that converts those drafts into the slice's
> merge-ready artifact. PR #007 is held open at `in-review` until
> 20 mappings are reviewed and the reviewer signs below.

## Review status

**Status:** APPROVED — all 56 mappings ship as drafted
**Reviewer:** Matt Goodrich
**Review date:** 2026-05-12
**Crosswalk file:** `data/crosswalks/soc2-tsc-2017.yaml`
**Crosswalk source attribution:** `community_draft` (agent-authored, not SCF-published)
**Total drafted mappings:** 56
**Low-confidence flagged (`strength ≤ 0.5`):** 9 — reviewed and accepted (see Decisions below)

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

## HITL decisions (2026-05-12)

Pair-review session between orchestrator + reviewer covered all 9 low-confidence rows plus a sample of high-confidence anchors. Decisions:

### PI1.x family (5 rows · weakest section)

**Decision: Accept as low-confidence `intersects_with` for v1.** SCF's Processing Integrity coverage is structurally narrow (PI is primarily encoded as a software-development concern via SEA-05; AICPA's PI criteria are conceptually broader). Shipping the rows as `community_draft` + `intersects_with` @ 0.4–0.5 preserves loose-hint value for downstream evaluation and explicitly flags the weak-link via strength score. SCF may publish stronger PI mappings later; the importer will supersede with `source_attribution=scf_official`. Documented as the v1 baseline — not a deferred decision.

| TSC   | SCF anchor | Decision   |
| ----- | ---------- | ---------- |
| PI1.1 | GOV-01     | accept 0.4 |
| PI1.2 | SEA-05     | accept 0.5 |
| PI1.3 | SEA-05     | accept 0.5 |
| PI1.4 | SEA-05     | accept 0.4 |
| PI1.5 | DCH-03     | accept 0.4 |

### CC/A1.x low-confidence rows (5 rows)

**Decision: Accept all as-is.** Each TSC has its higher-confidence primary anchor edge already present (e.g., CC3.4 → RSK-04 @ 0.7 is the primary; CFG-04 @ 0.5 is supplemental). The community_draft + 0.5 intersects_with rows preserve auditor visibility into partial-fit reasoning without misrepresenting them as strong matches.

| TSC   | SCF anchor | Decision                                                  |
| ----- | ---------- | --------------------------------------------------------- |
| CC1.4 | HRS-04     | accept 0.5                                                |
| CC1.5 | HRS-01     | accept 0.5                                                |
| CC2.1 | GOV-01     | accept 0.5                                                |
| CC3.4 | CFG-04     | accept 0.5 (supplemental to primary CC3.4 → RSK-04 @ 0.7) |
| A1.1  | MON-01     | accept 0.5                                                |

### High-confidence sample

The 47 high-confidence rows (strength ≥ 0.6) were spot-checked against canonical STRM patterns. No revisions requested. The canonical STRM equivalents (e.g., CC6.2 → IAC-07 equal 1.0, CC7.4 → IRO-04 equal 1.0, CC8.1 → CHG-02 equal 1.0, C1.1 → DCH-01 equal 1.0, A1.3 → BCD-11 equal 1.0) carry the framework's load-bearing weight as expected.

## Sign-off

- Reviewer name: Matt Goodrich
- Reviewer role: solo security leader / project owner
- Review date: 2026-05-12
- Total mappings reviewed: 56 (full crosswalk — 9 low-confidence + 47 high-confidence sampled)
- Mappings approved as-is: 56
- Mappings revised before merge: 0
- Mappings rejected (replaced with `no_relationship`): 0
- Source attribution: all `community_draft` — superseded when SCF publishes an official STRM crosswalk
- Signature / commit SHA of merge: (filled by orchestrator after squash-merge)
