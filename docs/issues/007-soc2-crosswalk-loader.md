# 007 — SOC 2 v2017 (TSC) crosswalk loader

**Cluster:** Catalog & UCF graph
**Estimate:** 1.5d
**Type:** HITL

## Narrative

Load the SOC 2 v2017 Trust Services Criteria as a `FrameworkVersion` and create STRM-typed edges from each TSC requirement to its SCF anchor(s). The mapping data comes from SCF's published crosswalk (SCF → SOC 2 derivation per NIST IR 8477 STRM methodology). For each SOC 2 requirement (e.g., `CC6.1`, `CC6.6`), insert rows in `fw_to_scf_edges` with `relationship_type` and `strength`. HITL: the mapping table needs human review (an auditor or compliance practitioner should validate ~20 spot-checks before we ship). The slice delivers value because the first end-to-end SOC 2-to-SCF traversal works — query CC6.6 → return its SCF anchors with strengths.

## Acceptance criteria

- [ ] AC-1: `just import-soc2 <path-to-crosswalk.json>` creates the `FrameworkVersion` row for SOC 2:2017 and ~60 framework_requirement rows
- [ ] AC-2: `fw_to_scf_edges` contains edges for every loaded SOC 2 requirement; each edge has `relationship_type ∈ {equal, subset_of, superset_of, intersects_with, no_relationship}` and `strength ∈ [0.0, 1.0]`
- [ ] AC-3: `GET /v1/requirements/SOC2:2017:CC6.6/anchors` returns one or more SCF anchors with strengths
- [ ] AC-4: Spot-check of 20 mappings is documented in `docs/audit-log/soc2-mapping-review.md` with reviewer name + date
- [ ] AC-5: Re-import is idempotent
- [ ] AC-6: Mapping source attribution stored on each edge (`source_attribution='SCF official' | 'community' | 'org-internal'`)

## Constitutional invariants honored

- **Invariant 1 (one control, N satisfactions):** edges go through SCF anchors only — never requirement-to-requirement directly
- **Invariant 7 (SCF canonical catalog):** crosswalk depends on slice 006
- **Invariant 8 (OSCAL wire format — partial):** mapping data structure is OSCAL-compatible for future export

## Canvas references

- `Plans/canvas/03-ucf.md` §3.1–3.2 (graph + STRM cardinality)
- `Plans/UCF_GRAPH_MODEL.md` §2, §4 (model + relationship types)

## Dependencies

- #006

## Anti-criteria (P0)

- Does NOT create direct requirement-to-requirement mappings (must go through SCF anchor)
- Does NOT silently default to `equal/1.0` for ambiguous mappings — surface unmapped requirements for human review
- Does NOT skip the spot-check review (HITL requirement)

## Skill mix (3–5)

- Go + sqlc
- STRM methodology (NIST IR 8477)
- Compliance domain knowledge (HITL reviewer)
- JSON crosswalk parsing
- Idempotent loader patterns
