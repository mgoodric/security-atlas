# 137 — Controls UCF graph data export (CSV / JSON / XLSX)

**Cluster:** Backend / Frontend
**Estimate:** 1d
**Type:** JUDGMENT (column-set design for graph data; engineer records D1)
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135. Controls UCF is the largest exportable surface — ~1,400 SCF anchors × framework-satisfaction edges × applicability_expr can easily reach 100,000+ rows. This slice ships the controls export with a **lifted row cap** (slice 135 default is 100,000; this slice's per-entity override is 500,000 with D1 justification).

**What this slice ships:** `GET /v1/admin/controls/export?format=<csv|json|xlsx>` reusing slice 135 library, with the row cap lifted to 500,000 at registration time. JUDGMENT call (D1) at pickup: which graph projection to export — flat (one row per `(control, framework_satisfaction)` edge), nested (one row per control with framework_satisfactions as a JSON-encoded column), or two-sheet XLSX (controls sheet + edges sheet)?

**Scope discipline (what is OUT):** framework definitions / version metadata export (separate); applicability_expr DSL pretty-printing (separate); OSCAL profile export (slice 030 territory); STRM mapping edges to non-SCF anchors (out of scope — UCF is the SCF spine).

## Threat model

Inherits slice 135. UCF-specific addendum:

| STRIDE                       | UCF-specific concern                                                                                                                                                     | Mitigation                                                                                                                                                                                                 |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **D** DoS                    | The 500K-row cap is 5× slice 135's default. A 500K × 10-column row at 200 bytes each = ~1 GB transfer. Streaming is mandatory; concurrency cap (slice 135) is mandatory. | Per-tenant concurrency cap from slice 135 inherited (max 2 in-flight). Streaming inherited. Engineer monitors p95 export latency at pickup and lowers the cap if 500K proves operationally too aggressive. |
| **I** Information disclosure | The UCF graph itself is NOT tenant-specific (SCF anchors are public); but `applicability_expr` values (scope tuples like `BU=Eng AND env=prod`) ARE tenant-private       | RLS enforcement on `applicability_expr` reads — same as slice 135. The applicability_expr column is included; the underlying scope tuple metadata IS tenant data.                                          |

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `GET /v1/admin/controls/export?format=...` reuses slice 135 library with `MaxRows = 500_000`.
- [ ] AC-2: D1 graph-projection JUDGMENT call (flat / nested / two-sheet XLSX) recorded in `docs/audit-log/137-controls-ucf-export-decisions.md`.
- [ ] AC-3: BFF route + Export button on the controls page.
- [ ] AC-4: Cross-tenant isolation integration test on `applicability_expr` reads.
- [ ] AC-5: OPA admit-set parity test.
- [ ] AC-6: Meta-audit row (action = `controls_export`).
- [ ] AC-7: Playwright e2e.
- [ ] AC-8: Streaming-memory test asserts under 200 MB for a 500K-row export.
- [ ] AC-9: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 135. Adds: **#1 one control, N framework satisfactions** — export structure preserves the graph shape (chosen projection from D1 makes this legible).

## Canvas references

- `Plans/canvas/03-ucf.md` — UCF graph model; D1 picks the projection that best preserves the graph for downstream tooling.
- `Plans/UCF_GRAPH_MODEL.md` — full graph deep-dive; reference for column-set design.

## Dependencies

- **#135** Data-export library. **Gate: 135 merged.**
- Slice 006 (SCF catalog importer, merged) — the SCF anchor surface this exports.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 P0-A1 through P0-A14.
- **P0-A-UCF-1:** Row cap 500,000; NOT removable; the per-entity override is justified in D1.
- **P0-A-UCF-2:** Two-sheet XLSX (if D1 picks it) MUST still satisfy slice 135 P0-A6 — TWO sheets allowed (controls + edges); NO chart objects, NO named ranges, NO VBA.
- **P0-A-UCF-3:** Streaming test asserts memory cap at 200 MB for a 500K-row export (5× slice 135 cap because of cap lift).

## Skill mix

- slice 135's `internal/export/` library — consume only.
- Go integration tests + Playwright e2e.
- XLSX library / handcrafted writer extension (D1 dependent).

## Notes for the implementing agent

This is the largest-surface export. The D1 projection call shapes everything downstream — pick the projection that best serves the most common operator workflow (likely flat edges, since that's what Excel pivots want).

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135.
