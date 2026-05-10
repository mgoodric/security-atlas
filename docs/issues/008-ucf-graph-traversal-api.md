# 008 — UCF graph traversal query API

**Cluster:** Catalog & UCF graph
**Estimate:** 2d
**Type:** AFK

## Narrative

Implement the bidirectional graph traversal that powers the dashboard, control detail, and questionnaire flows. Two query directions: (1) **forward** — `evidence → control → SCF anchor → framework requirements satisfied`, used after ingest to compute what new framework satisfactions just lit up; (2) **reverse** — `framework requirement → SCF anchor(s) → control(s) → evidence`, used to answer "what evidence do we have for SOC 2 CC6.6?". Both traversals are Postgres recursive CTEs against the `fw_to_scf_edges` table and the `controls.scf_anchor_id` field. Expose as REST endpoints with version-pinning to a `FrameworkVersion` and `scf_release_id`. The slice delivers value because the dashboard and control detail views can render real coverage data.

## Acceptance criteria

- [ ] AC-1: `GET /v1/requirements/SOC2:2017:CC6.6/coverage?as-of=<timestamp>` returns the requirement, its SCF anchors with strengths, and the controls anchored to each with their effectiveness scores
- [ ] AC-2: `GET /v1/anchors/SCF:IAC-06/requirements?framework_version=SOC2:2017` returns satisfied framework requirements with strengths
- [ ] AC-3: `GET /v1/controls/:id/coverage?framework_version=SOC2:2017` returns all framework requirements this control satisfies with computed coverage
- [ ] AC-4: Traversal respects `framework_version_id` and `scf_release_id` — historical version queries return historical mappings
- [ ] AC-5: Query latency under 200ms for typical org volumes (verified with a benchmark test seeding 1,400 anchors + 60 SOC 2 reqs)
- [ ] AC-6: All responses tenant-scoped via RLS

## Constitutional invariants honored

- **Invariant 1 (one control, N satisfactions):** traversal produces N framework satisfactions from one control without duplication
- **Invariant 6 (RLS):** all queries operate within the RLS-enforced tenant context

## Canvas references

- `Plans/canvas/03-ucf.md` §3.1, §3.2
- `Plans/UCF_GRAPH_MODEL.md` §7 (bidirectional traversal queries), §8 (Postgres-not-Neo4j storage)

## Dependencies

- #002, #006, #007

## Anti-criteria (P0)

- Does NOT cache results across tenants
- Does NOT swallow RLS denials silently — surface as 404 / empty list per route
- Does NOT model framework-to-framework relationships directly (must traverse via SCF anchor)

## Skill mix (3–5)

- Postgres recursive CTEs
- sqlc-typed query layer
- REST API + Go handlers
- Performance benchmarking (testing.B)
- Query plan analysis (EXPLAIN)
