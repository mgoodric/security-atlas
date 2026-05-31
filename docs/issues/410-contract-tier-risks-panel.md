# 410 — Contract-tier rollout: dashboard top-risks panel (GET /v1/risks)

**Cluster:** Quality
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** 409 (contract-tier dashboard rollout) · feeds 394 residual coverage

## Narrative

Surfaced during slice 409, captured per continuous-batch policy.

Slice 409 rolled the golden-file contract tier (ADR-0007) out to the five
dashboard panel routes whose handlers took a bounded Option-A read seam.
It **deferred** the dashboard top-risks panel (`GET /v1/risks`, consumed
by `web/app/api/dashboard/risks/route.ts` via `getMitigateRisks`) for two
reasons documented in `docs/audit-log/409-contract-tier-rollout-dashboard-decisions.md`
D1:

1. **Wide handler surface.** The risks `Handler` (`internal/api/risks/
handlers.go`) holds `store *risk.Store` exposing Create / List / Get /
   Delete / Heatmap / ThemeOrgUnitHeatmap and more. An Option-A read seam
   for the one `ListRisks` endpoint would be a ~7-method interface — a
   bigger refactor than recording one golden justifies. The clean shape is
   a **list-only sub-interface** (just the `List` method the handler's
   `ListRisks` path uses) the recorder injects, leaving the rest of the
   handler on the concrete `*risk.Store`.
2. **Non-passthrough BFF.** Unlike the slice-409 dashboard panels, the
   dashboard/risks BFF is NOT a verbatim passthrough — it unwraps
   `body.risks` and re-wraps `{risks, count}`. The provider golden pins the
   upstream `/v1/risks` envelope (`riskWire[]` + `count`); the consumer
   assert must be **transform-aware** (assert the BFF's re-wrapped output
   matches `{risks: golden.risks, count: golden.risks.length}`), not a
   `toEqual(golden)`.

## What ships

- An unexported list-only read seam on the risks `Handler` (Option A,
  P0-409-2 — stays internal, `New(*risk.Store)` unchanged).
- A provider recorder (`internal/api/risks/*_contract_test.go`) reusing the
  shared recorder helper, recording the `riskWire` envelope (incl. the
  opaque `inherent_score` / `residual_score` JSON blobs, the
  `linked_control_ids` array, and the nullable `review_due_at` /
  `accepted_until`).
- A transform-aware consumer assert
  (`web/lib/contracts/risks.contract.test.ts`).
- Drift-sensitivity proof on one renamed field.

## Acceptance criteria

- [ ] **AC-1.** List-only read seam on the risks handler; recorder on the
      unit surface (no DB; no integration tag — ADR-0007 / P0-409-1).
- [ ] **AC-2.** Golden + transform-aware consumer assert land.
- [ ] **AC-3.** Drift sensitivity proven on ≥1 field.
- [ ] **AC-4.** Zero-new-gate (rides Go-unit + vitest).

## Dependencies

- **#409** — `merged`. Established the dashboard Option-A seam pattern + the
  shared recorder helper this slice copies.
- **#394** — consumes this golden once it lands (residual dashboard route).

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 409 decisions log D1 + D6 (the deferral rationale)
