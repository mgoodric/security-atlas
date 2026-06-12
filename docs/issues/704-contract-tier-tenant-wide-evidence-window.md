# 704 — Contract-tier rollout: tenant-wide `/v1/evidence` ledger window (the slice-106 + 234 filter-matrix branch slice 692 deferred)

**Cluster:** Quality
**Estimate:** 0.5-1d
**Type:** JUDGMENT
**Status:** `ready` (deps merged when slice 692 lands — the per-route
Option-A seam + recorder scaffolding in `internal/api/controldetail` is the
direct precedent this slice extends)
**Parent:** 692 (controldetail attest-form + per-control Evidence window) ·
690 · 689 · 687 · 412 · 411 · 409 (rollout origin)

## Narrative

Surfaced during slice 692, captured as a follow-up per continuous-batch policy.

Slice 692 drained the two control-detail reads slice 690 deferred. As the
JUDGMENT owner it scoped the `internal/api/controldetail` `Evidence` seam to
the **per-control branch only** (`GET /v1/evidence?control_id=…`) and
deliberately deferred the **tenant-wide ledger window** (`GET /v1/evidence`
with no `control_id` — the slice-106 branch plus the slice-106 optional
filters and the slice-234 `scope_cell_id` filter). See slice 692 decisions log
D3.

Rationale for the deferral (recorded so the cut is auditable): the `/e2e/`
suite traverses the per-control evidence card on the control-detail surface,
not the tenant-wide `/evidence` list view from that surface; and the
tenant-wide branch is its own filter-heavy recorder story — a distinct
`dbx.ListEvidencePagedRow` row type plus a six-predicate optional-filter matrix
(`kind`, `result`, `source_actor_type`, `source_actor_id`, `scope_cell_id`,
plus the `[since, until]` window) — materially bigger than the per-control
window fixture. A clean coherent cut + a spillover beats an overreaching slice.

## What ships (when picked up)

A per-route Option-A read seam over the tenant-wide branch of the `Evidence`
handler (`internal/api/controldetail/handler.go`) + a provider recorder (no DB,
no integration tag — P0-409-1) + a consumer assert + a drift proof.

- **`GET /v1/evidence`** (no `control_id`) — `internal/api/controldetail`
  `Evidence`, the tenant-wide ledger window. Currently reads
  `h.store.EvidencePaged(ctx, evidenceListPage{…})` → `[]dbx.ListEvidencePagedRow`
  plus `h.store.CountEvidenceForTenant(ctx)`. Extend the slice-692
  `evidenceWindowReader` seam (or add a sibling `evidenceTenantWideReader`
  seam — implementer's JUDGMENT) with `EvidencePaged`. Pin the same
  `evidenceWire` row shape (`evidence_id` / `evidence_kind` nullable /
  `observed_at` / `source` JSONB / `content_hash` / `scope_cell` nullable /
  `result`) + the `{control_id: "", evidence, count, total, next_cursor}`
  envelope (note `control_id` is the empty string on this branch). Record at
  least one variant that exercises a non-empty filter predicate so the
  filter-matrix wire is pinned, and one empty-window variant.
- **Consumer disposition** — the BFF (`web/app/api/evidence/route.ts`) is a
  verbatim passthrough (it forwards upstream body bytes + status unchanged), so
  the consumer half is a toEqual BFF-drive (same disposition as slice 692's
  per-control branch). Confirm at pickup.

## Acceptance criteria

- [ ] **AC-1.** Tenant-wide read seam on the `controldetail` `Evidence`
      no-`control_id` path; recorder on the unit surface (no DB; no integration
      tag — P0-409-1). Public constructor unchanged (P0-409-2:
      `controldetail.New`).
- [ ] **AC-2.** Golden + consumer assert (toEqual BFF-drive — verbatim
      passthrough — unless the BFF disposition has changed by pickup).
- [ ] **AC-3.** Drift sensitivity proven on the new endpoint.
- [ ] **AC-4.** Zero-new-gate (no `ci.yml` change; rides Go-unit + vitest).

## Dependencies

- **#692** — `merged` (when this lands). Built the per-control `Evidence` seam
  - the `controldetail` recorder scaffolding this slice extends, and recorded
    the per-control half of the same envelope.
- **#690** / **#689** / **#687** / **#411** / **#412** / **#409** /
  **ADR-0007** — the per-route seam pattern + the Option-A seam constraint +
  the passthrough-vs-field-contract consumer disposition + the origin tier.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 692 decisions log
  (`docs/audit-log/692-contract-tier-controldetail-decisions.md`) — D3 (the
  per-control vs tenant-wide seam-scope JUDGMENT call this slice closes)
