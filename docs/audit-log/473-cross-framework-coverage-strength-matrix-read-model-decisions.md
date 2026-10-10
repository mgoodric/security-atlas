# OE-473 / 402a — Cross-framework coverage-strength read-model decisions

Slice type: JUDGMENT. The row axis, cell aggregation rule, and visual band
token mapping are product calls. This slice records them so 402b consumes a
settled backend contract instead of re-deriving the matrix semantics in UI code.

- detection_tier_actual: none
- detection_tier_target: none

## D1 — Row axis: SCF anchors, not control families

**Chosen:** matrix rows are SCF anchors.

**Rationale:** the feature exists to demonstrate constitutional invariant #1:
one control, N framework satisfactions through the SCF spine. An SCF-anchor row
is the exact grain where one evaluated control fans out into multiple
frameworks. A control-family row would be a second aggregation layer over
anchors and would hide which specific anchor is weak.

**Rejected:** control families as the primary backend row axis. Families are a
useful future grouping/filter, but making them the read-model grain would force
another subjective rollup across anchors before 402b can render anything.

## D2 — Columns: current framework versions

**Chosen:** columns are current framework versions, excluding SCF itself.

**Rationale:** slice 484 established that unpinned coverage reads default to
current framework versions. The matrix is an at-a-glance operator posture view,
not a historical version comparison. Legacy versions remain reachable through
pinned traversal endpoints; this matrix keeps the wide view bounded and current.

## D3 — Cell aggregation: max requirement contribution

**Chosen:** each cell is:

```text
MAX over mapped requirements in that framework of
  edge_strength * anchor_coverage_for_that_framework
```

where `anchor_coverage_for_that_framework` is computed by reusing slice 482's
RLS-scoped control/effectiveness/framework-scope rollup primitive. This is
aggregation of the existing rollup contribution, not new coverage evaluation.

**Rationale:** a cell answers "how strongly does this SCF anchor contribute to
this framework?" If multiple requirements in a framework map to the same anchor,
the strongest mapped contribution is the honest cell headline and matches slice
482's best-satisfying-path rule. Sum can exceed 1.0; average can penalize an
anchor for extra weak mappings; minimum overstates weakness when one requirement
has a weaker alternate path.

## D4 — Band mapping: semantic status tokens

**Chosen:** the backend emits the band and the semantic status token:

| Coverage band | Semantic token |
| ------------- | -------------- |
| `strong`      | `pass`         |
| `partial`     | `warning`      |
| `weak`        | `critical`     |
| `uncovered`   | `info`         |

**Rationale:** slice 753's token family is the stable UI contract. 402b should
render cells by token, not by hardcoded palette utilities. `strong → pass`
matches successful coverage, `partial → warning` marks incomplete but useful
coverage, `weak → critical` flags a high-attention gap, and `uncovered → info`
keeps absence of a contributing path distinct from a failing weak path.

## D5 — Bounded backend shape

**Chosen:** `GET /v1/coverage-strength/matrix` pages anchor rows with
`limit`/`offset` and optional `family`, returning `{axis, bands, frameworks,
rows, limit, offset}`. The route is backend/read-model only; no UI is built in
this slice.

**Rationale:** the original matrix issue's DoS note calls for bounded reads.
Paging by anchor rows gives 402b deterministic row pages without duplicating
controls per framework or asking the UI to discover the axis itself.
