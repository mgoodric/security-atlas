# 537 — Cross-framework coverage-strength comparison matrix

**Cluster:** Catalog
**Estimate:** M (1-2d) — multi-framework read-model aggregation + matrix UI

**Type:** JUDGMENT (the matrix axis/aggregation choices + the cell-band display
are subjective product calls)

**Status:** `deferred` (the per-requirement rollup landed in slice 482; the
cross-framework matrix is a richer phase-2 surface — pick up when an operator
wants the at-a-glance "which framework am I weakest on" view)

> Filed 2026-06-07 as a spillover of slice 482 (coverage-strength rollup +
> visualization). Parent: slice 482. Slice 482's P0-482-6 explicitly carved
> out the cross-framework comparison matrix as a follow-on; slice 482 did the
> per-requirement rollup, surfaced wherever a requirement's coverage is
> already shown.

## Narrative

**Why.** Slice 482 answers "how strongly is _this_ requirement covered?" one
requirement at a time. The constitutional headline (invariant #1: one control,
N framework satisfactions) implies a more powerful view: because a single SCF
anchor's evaluated coverage feeds **every** framework requirement it satisfies,
the platform can show a **cross-framework matrix** — rows = SCF anchors (or
control families), columns = frameworks (SOC 2 / ISO / PCI / CSF / HIPAA), cells
= the coverage-strength contribution — so the operator sees at a glance "I'm
strong on SOC 2 but weak on PCI for the same control family." This is the
invariant-#1 payoff made visual across frameworks, not just per requirement.

**What.** A read-model + matrix UI that:

- Aggregates the slice-482 per-requirement coverage-strength across the
  installed frameworks into a matrix keyed by (anchor-or-family × framework).
- Renders each cell with the slice-482 confidence band (reusing
  `web/components/control/confidence-band.ts` — no new band vocabulary).
- Lets the operator spot the weakest framework for a given control area.

**Scope discipline.** Reuses the slice-482 formula + bands verbatim (no new
scoring). Does NOT ship the crosswalk-review/editing UI (slice 536). The matrix
is a read+visualize surface like slice 482.

## Threat model (STRIDE)

The matrix folds in the **tenant's evaluated coverage state across many
requirements at once** — the same tenant-confidential derived data as slice
482, at a wider fan-out. Threat-model I is again the dominant category.

**S — Spoofing.** Reuses the existing bearer/role read gate; no new ingress
beyond a read endpoint behind the viewer role.
**Mitigation:** reuse the existing read auth.

**T — Tampering.** Cells are server-computed from DB values; no client-supplied
strength (the slice-482 P0-482-4 rule, carried forward).
**Mitigation:** server computes every cell from `fw_to_scf_edges` × the
RLS-scoped tenant state; query params are the existing validated set.

**R — Repudiation.** A derived display value, not an audit-binding artifact;
no new repudiation surface.
**Mitigation:** none required; underlying edges + evaluations remain auditable.

**I — Information disclosure (the load-bearing category).** The matrix is
**tenant-confidential derived data** — it must be computed only over RLS-scoped
control/state rows, exactly as slice 482 does, but now across many requirements
in one response. A bug that computed any cell outside RLS would leak how well
another tenant covers a whole framework. A second tenant's matrix must reflect
ITS state (or uncovered), never tenant A's.
**Mitigation:** the matrix read runs entirely inside the tenant
`app.current_tenant` GUC; an integration test asserts a second tenant sees its
own (or uncovered) matrix, never tenant A's — the slice-482 AC-7 assertion
generalized to the matrix.

**D — Denial of service.** The matrix is O(anchors × frameworks) — wider than
slice 482's single-requirement fan-out. The read MUST be bounded (paginate by
control family or anchor page) so it can't become an unbounded all-requirements
scan.
**Mitigation:** paginate the matrix rows; cap the fan-out per request; reuse the
slice-482 per-control effectiveness computation (computed once per control,
reused across the cells it feeds).

**E — Elevation of privilege.** Read-only; no new capability.
**Mitigation:** reuse the existing read role boundary.

## Constitutional invariants honored

- **#1 — One control, N framework satisfactions.** The matrix IS this
  invariant visualized: a shared anchor's coverage feeds every framework column
  it satisfies.
- **#6 — Tenant isolation enforced at the DB layer (RLS).** Every cell is
  computed only over RLS-scoped rows (threat-model I).
- **#7 — Mappings go requirement → SCF anchor.** The matrix reads existing
  edges; it creates none.

## Dependencies

- **#482** (coverage-strength rollup + visualization) — supplies the
  per-requirement formula + the confidence-band vocabulary this matrix reuses.
- **#438** (generic crosswalk loader) — supplies the multi-framework edges.

## Canvas references

- `Plans/canvas/10-roadmap.md` §10.2 — "Coverage-strength visualization across
  frameworks" named in phase-2 (slice 482 did per-requirement; this is the
  across-frameworks matrix).
- `Plans/canvas/03-ucf.md` §3.2 — the coverage-strength model the matrix
  aggregates.
