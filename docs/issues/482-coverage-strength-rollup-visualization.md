# 482 — Coverage-strength rollup + mapping-confidence visualization

**Cluster:** Catalog
**Estimate:** L (2-3d) — read-model rollup (backend) + BFF + UI surface
**Type:** JUDGMENT (the rollup formula + the confidence-band thresholds are
subjective product calls)
**Status:** `ready`

## Narrative

The UCF's headline promise (canvas §3.2) is that the platform can compute
**coverage _strength_ per requirement** through the STRM-edge `strength` field:

> "if your evidence covers SCF:IAC-22 with strength 1.0, and ISO27001:A.9.4.2 →
> SCF:IAC-22 with strength 0.8, the ISO requirement is covered at 0.8, and the
> UI surfaces the gap."

Today the raw ingredient exists but the rollup and its visualization do not.
`fw_to_scf_edges.strength` is stored on every edge (the slice-438 loader
requires an explicit `strength` per row — no silent `1.0`), and
`GET /v1/requirements/{id}/coverage` already returns each anchor's `strength`
in its `anchors[]` array (`internal/api/ucfcoverage/requirement_coverage.go`).
But:

1. There is **no strength-weighted coverage score per requirement** — the
   product never combines edge strength with the anchor's evidence
   state/freshness to answer "how strongly is ISO A.9.4.2 actually covered?"
   The caller gets raw per-anchor strengths and must compute the rollup itself.
2. There is **no UI surface** that visualizes mapping confidence. Slice 438
   explicitly deferred "coverage-strength visualization" (438 narrative + its
   P0-438-6); slice 447 reaffirmed the deferral; roadmap §10.2 names
   "Coverage-strength visualization across frameworks" as phase-2 work. This is
   that slice.

This slice ships **(a) a read-model that computes a per-requirement
coverage-strength score** — the edge strength combined with the anchor's
evaluated coverage state (the slice-012 control-state + slice-016 freshness
signals already exist) — exposed on the existing
`/v1/requirements/{id}/coverage` payload as an additive
`coverage_strength` field plus a `confidence_band` label, and **(b) a UI
surface** on the control/requirement detail view that renders the per-anchor
strength + the rolled-up requirement coverage-strength with a confidence band
(e.g. strong / partial / weak / uncovered) so the operator can see the gap the
canvas describes.

**The rollup formula (the JUDGMENT call).** The canvas worked example takes the
_weakest link_: ISO→anchor at 0.8 with anchor evidence at 1.0 yields ISO
covered at 0.8. The natural generalization when a requirement maps through
multiple anchors is "best satisfying path" (max over anchors of
`edge_strength × anchor_coverage`) with the per-anchor term itself being a
min/product of edge strength and the anchor's evaluated state. The implementing
agent owns the exact formula and the band thresholds; both are recorded in the
decisions log and explicitly flagged "Revisit once in use" because real
auditor feedback will tune them.

**Scope discipline.** This is the **read + visualize** slice, not a crosswalk
_review/editing_ tool. It does **not** ship the canvas §10.2 "crosswalk
validation tooling (UI for reviewing STRM mappings, surfacing conflicts)" —
that is a separate slice about _editing/approving_ mappings. It does **not**
add a cross-framework comparison matrix view (a richer phase-2 surface; this
slice does per-requirement rollup, surfaced wherever a requirement's coverage
is already shown). It does **not** change how strength is _stored_ or how the
438 loader _writes_ it. **Follow-on slices:** crosswalk-review/conflict UI;
cross-framework coverage-strength comparison matrix.

## Threat model (STRIDE)

This slice adds a computed field to an existing read endpoint + a UI surface.
The endpoint is tenant-context-aware: anchors/requirements are global catalog
data, but the **rollup folds in the tenant's evaluated coverage state**, so the
`coverage_strength` value is **tenant-scoped derived data** and must respect
RLS exactly as the existing `controls[]` array does.

**S — Spoofing.** No new endpoint; the additive field rides the existing
`GET /v1/requirements/{id}/coverage` bearer/role gate. The UI surface reuses
the existing control/requirement-detail auth. No new ingress.
**Mitigation:** reuse the existing endpoint auth; no new route.

**T — Tampering.** The `coverage_strength` value is computed server-side from
`fw_to_scf_edges.strength` (catalog) × the tenant's evaluated state; the client
cannot influence it beyond the existing query params (`?as-of`,
`?framework_version`). A client supplying a crafted param must not coerce a
higher score.
**Mitigation:** the rollup is computed in the handler from DB values, never
from request body; query params are the existing validated set; no
client-supplied strength is accepted.

**R — Repudiation.** Not an audit-binding write path; no new repudiation
surface. The score is a derived display value, not a stored artifact.
**Mitigation:** none required; the underlying edge strengths remain auditable
via the 438 import log.

**I — Information disclosure.** The load-bearing category. The rollup folds in
the tenant's evaluated coverage state — so `coverage_strength` is
**tenant-confidential derived data**, NOT global catalog data. A bug that
computed it outside RLS, or surfaced it for a foreign tenant's controls, would
leak how well another tenant covers a requirement.
**Mitigation:** the rollup MUST be computed only over RLS-scoped control/state
rows (the same scope the existing `controls[]` array uses — see the handler's
"controls array is RLS-scoped" note). For a requirement where the tenant has no
controls, `coverage_strength` resolves to the uncovered band (empty, not a
foreign tenant's value). **Integration test asserts a second tenant sees its
own (or uncovered) score, never tenant A's.**

**D — Denial of service.** The rollup is a bounded computation over one
requirement's anchors + the tenant's controls on those anchors — the same
fan-out the endpoint already does.
**Mitigation:** no unbounded scan; the rollup adds an O(anchors) pass over data
already fetched. No new query that lists all requirements' scores at once.

**E — Elevation of privilege.** No new capability; read-only field on a read
endpoint behind the existing role gate.
**Mitigation:** reuse the existing read role boundary.

## Acceptance criteria

**Backend — coverage-strength rollup (read model)**

- [ ] **AC-1.** `GET /v1/requirements/{id}/coverage` gains an additive
      `coverage_strength` numeric field (0.0–1.0) and a `confidence_band`
      string label, computed server-side from the per-anchor edge `strength`
      combined with each anchor's tenant-evaluated coverage state. The existing
      payload fields are unchanged (additive only).
- [ ] **AC-2.** The rollup is tenant-RLS-scoped: it folds in only the tenant's
      own control/state rows (the same scope as the existing `controls[]`
      array). A requirement the tenant has no controls for resolves to the
      uncovered band.
- [ ] **AC-3.** The per-anchor `strength` already returned stays; AC-1's
      requirement-level score is the rollup _over_ those anchors via the
      documented formula.

**Frontend — mapping-confidence visualization**

- [ ] **AC-4.** The control/requirement detail view renders, per requirement,
      (a) each mapped anchor's STRM relationship type + edge strength and
      (b) the rolled-up `coverage_strength` with a visible confidence band
      (e.g. strong / partial / weak / uncovered), so the operator sees the gap
      the canvas §3.2 example describes.
- [ ] **AC-5.** The BFF route + `web/lib/api.ts` type carry the new
      `coverage_strength` + `confidence_band` fields; vitest covers the BFF
      mapping; a Playwright assertion covers the band rendering on the detail
      view.

**Tests**

- [ ] **AC-6.** Integration test (`//go:build integration`): the rollup against
      real Postgres for a requirement with mixed-strength edges + mixed
      evidence state produces the expected band.
- [ ] **AC-7.** Integration test (threat-model I): a second tenant's
      `coverage_strength` for the same requirement reflects ITS state (or
      uncovered), never tenant A's.
- [ ] **AC-8.** Pure-Go unit test (`helpers_test.go` pattern) covers the rollup
      formula + band-threshold branches without a DB (fast table tests).

**Docs / JUDGMENT artifact**

- [ ] **AC-9.** A decisions log
      (`docs/audit-log/482-coverage-strength-rollup-decisions.md`) records the
      chosen rollup formula, the band thresholds, why (pattern-matched to the
      canvas §3.2 weakest-link example), confidence per decision, and a "Revisit
      once in use — auditor feedback will tune the formula + bands" list.
      Include the `detection_tier_actual` / `detection_tier_target` header.
- [ ] **AC-10.** A changelog entry for the slice.

## Constitutional invariants honored

- **#1 — One control, N framework satisfactions.** The rollup is computed
  _through_ shared SCF anchors; a single anchor's strength contributes to every
  framework requirement it satisfies — the invariant made visible.
- **#6 — Tenant isolation enforced at the DB layer (RLS).** The rollup folds in
  tenant-evaluated state and MUST be computed only over RLS-scoped rows
  (threat-model I).
- **#7 — Mappings go requirement → SCF anchor.** The rollup reads existing
  requirement → anchor edges; it creates none.

## Canvas references

- `Plans/canvas/03-ucf.md` §3.2 — the coverage-strength promise + the
  ISO-A.9.4.2-at-0.8 worked example this slice implements.
- `Plans/canvas/10-roadmap.md` §10.2 — "Coverage-strength visualization across
  frameworks" named in phase-2.
- Slice 438 narrative + P0-438-6 — coverage-strength visualization explicitly
  deferred to this follow-on.

## Dependencies

- **#438** (generic crosswalk loader) — `merged`. Establishes the per-edge
  `strength` field this slice rolls up.
- **#012** (control-state evaluation) — `merged`. Supplies the anchor coverage
  state the rollup combines with edge strength.
- **#016** (evidence freshness + drift) — `merged`. Optional input to the
  rollup if the formula weights freshness (decisions-log call).
- The `/v1/requirements/{id}/coverage` endpoint
  (`internal/api/ucfcoverage`) — exists; this slice extends its payload.

## Anti-criteria (P0 — block merge)

- **P0-482-1.** Does NOT ship the crosswalk-review/conflict editing UI (canvas
  §10.2 "crosswalk validation tooling") — that is a separate slice. This is
  read + visualize only.
- **P0-482-2.** Does NOT change how `strength` is stored or how the 438 loader
  writes edges.
- **P0-482-3.** The `coverage_strength` rollup MUST NOT be computed or surfaced
  outside RLS — no foreign-tenant state leak (threat-model I; AC-2 + AC-7).
- **P0-482-4.** Does NOT accept any client-supplied strength value (threat-model
  T) — the score is server-computed from DB values only.
- **P0-482-5.** Additive only — does NOT remove or rename existing
  `/coverage` payload fields.
- **P0-482-6.** Does NOT ship a cross-framework comparison matrix view —
  per-requirement rollup only; the matrix is a follow-on.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; the RLS tenant-isolation
assertion is load-bearing) · `database-designer` (the rollup query joining
edges × evaluated state under RLS) · `security-review` (tenant-derived field on
a catalog read path) · `simplify`.

## Notes for the implementing agent

- The raw materials all exist: edge `strength` (438), anchor coverage state
  (012), freshness (016), and the `/coverage` endpoint. The work is the rollup
  computation, the additive payload fields, the RLS-scoping discipline, and the
  UI band.
- **JUDGMENT calls you own:** the exact rollup formula (start from the canvas
  weakest-link example, generalize to best-satisfying-path over multiple
  anchors), the confidence-band thresholds, and the UI band labels. Record all
  three in the decisions log with `low`/`medium` confidence — these are the
  things real auditors will push back on first.
- AC-7 is the load-bearing security assertion — the rollup folds in tenant
  state, so it MUST be RLS-scoped; prove a second tenant never sees tenant A's
  score.
- Detection-tier classification: set both fields to `none` unless a bug
  surfaces during the build.
