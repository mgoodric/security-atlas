# 661 — Global search returns no results for controls / SCF anchors

**Cluster:** Search
**Estimate:** M (1-2d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-002).

## Narrative

Header search ("Search controls, evidence, risks…") matches **nothing** for control or
SCF-anchor queries even though the records exist. `q=encryption` and exact `q=CRY-04` both
return `200` with **empty** results; CRY-01/04/08 are present in `/controls` + `/catalog/scf`.
Re-verified on `main` build `2a3805b`.

**Orchestrator code triage (2026-06-10):** `internal/api/search/search.go` `searchControls`
queries the **`controls`** table (`WHERE superseded_by IS NULL`) — i.e. **instantiated
controls only** — and there is **no `scf_anchors` (catalog) search branch at all**
(`searchType` dispatches controls/risks/evidence only). On a fresh/empty tenant there are
zero instantiated controls; the ~53 SCF anchors live in the catalog and are unindexed → the
operator's most natural query ("find CRY-04") returns nothing. This is a real indexing gap,
independent of deployment.

Secondary: the audit noted the `CRY-04` request hung "pending" for several seconds (latency).

## Threat model

Search must stay **tenant-scoped via RLS** (invariant #6) — the SCF anchor catalog is
tenant-agnostic reference data, so adding an anchor branch must read the catalog (not
cross-tenant tenant data) and must not leak another tenant's instantiated controls. Keep
the existing `queryInTenantTx` RLS binding for tenant tables.

## Acceptance criteria

- [ ] **AC-1.** Search matches **SCF anchors** by anchor code (e.g. `CRY-04`) AND by
      name/description (e.g. `encryption`), returning hits that link to the catalog/control
      surface. Add a `searchAnchors` branch over the bundled `scf_anchors` catalog.
- [ ] **AC-2.** Search continues to match **instantiated controls** (the existing branch),
      and a tenant with both sees both, de-duplicated sensibly (anchor vs its instantiated control).
- [ ] **AC-3.** A new hit `Type` (e.g. `anchors`/`catalog`) is wired through the result
      contract + the FE result rendering so anchor hits are labeled and navigable.
- [ ] **AC-4.** Integration test: on an empty tenant (no instantiated controls), `CRY-04`
      and `encryption` return anchor hits; RLS isolation still holds for tenant-scoped types.
- [ ] **AC-5.** Investigate + note the `CRY-04` latency (was it cold-start, a slow query, or
      a missing index?); add an index or bound if it's a real per-query cost.

## Anti-criteria

- Does NOT break RLS tenant-scoping for controls/risks/evidence search (catalog read is the
  only tenant-agnostic addition).
- Does NOT add a new evidence_kind, migration to tenant tables, or wire change beyond the
  search result `Type` enum + FE rendering.

## Dependencies

- `internal/api/search` (slice that added `/api/search`) — on `main`.
- The bundled SCF catalog (`scf_anchors`) — on `main`.

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-002** (priority high /
severity major). Re-tested open on build `2a3805b`. Two sub-issues: (a) anchor/control
indexing gap (primary); (b) per-query latency.
