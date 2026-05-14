# 030 — OSCAL SSP + POA&M export pipeline

**Cluster:** Audit workflow
**Estimate:** 4–5d
**Type:** JUDGMENT

## Narrative

Implement the OSCAL export pipeline that produces the audit-handoff bundle: System Security Plan (SSP), Assessment Plan + Assessment Results (AP/AR), and Plan of Action and Milestones (POA&M). The data comes from across the platform: org profile + scope cells + applicable controls + control implementations + linked policies + sample populations + walkthroughs + audit comments + findings. Use IBM `compliance-trestle` (Python, bridged via gRPC) for OSCAL JSON v1.1.x serialization. Export bundles are cosign-signed for tamper detection at handoff. The slice ships a spec-compliant bundle validated by `compliance-trestle` round-trip; whether a given auditor's tooling imports it cleanly is the kind of thing only real use surfaces — so the decisions log records the conformance choices made and flags "validate against a real auditor's tooling" as the top revisit item. The slice delivers value because the primary persona's SOC 2 audit handoff is now a single signed bundle — the binary v1 success test depends on this.

## Acceptance criteria

- [ ] AC-1: `oscal-export` CLI generates an SSP for an `AuditPeriod` as OSCAL JSON v1.1.x
- [ ] AC-2: SSP includes: org profile, scope cells, control implementations (from slice 010 + 012), linked policies (from slice 022)
- [ ] AC-3: Assessment Plan + Assessment Results generated from sample populations (slice 026) + walkthroughs (slice 027) + audit comments (slice 029)
- [ ] AC-4: POA&M generated from open findings with milestones, owners, due dates
- [ ] AC-5: Export bundle is cosign-signed; signature included in metadata
- [ ] AC-6: IBM compliance-trestle round-trip validation passes
- [ ] AC-7: `compliance-trestle` round-trip validation passes for SSP + AP/AR + POA&M; the OSCAL-conformance decisions (model-version choices, optional-field handling, any spec-ambiguity calls) are recorded in `docs/audit-log/030-oscal-ssp-poam-export-decisions.md` with "validate against a real auditor's tooling" as the top revisit item
- [ ] AC-8: `oscal-export` and audit-pack PDF available via UI + CLI

## Constitutional invariants honored

- **Invariant 8 (OSCAL wire format):** export honors the canonical OSCAL models
- **Invariant 10 (audit-period freezing):** export pulls from a frozen audit period

## Canvas references

- `Plans/canvas/03-ucf.md` §3.4 (OSCAL ingest and export)
- `Plans/canvas/08-audit-workflow.md` §8.2 (OSCAL SSP/POA&M export)

## Dependencies

- #008, #012, #017, #018, #022, #026, #028

## Anti-criteria (P0)

- Does NOT skip cosign signing of export bundle
- Does NOT export from a non-frozen period
- Does NOT permit AI auto-generation of SSP narrative without human approval per section — this is the **product runtime AI-assist boundary** (CLAUDE.md), unchanged and constitutional; distinct from the dev-process JUDGMENT model
- Does NOT skip `compliance-trestle` round-trip validation

## Skill mix (3–5)

- Go + gRPC bridge to Python compliance-trestle
- OSCAL JSON v1.1.x schema
- cosign / sigstore for signing
- Compliance domain knowledge
- CLI design (cobra commands)
