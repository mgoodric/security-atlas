# 106 — `GET /v1/evidence` backend extension (spillover from 099)

**Cluster:** Backend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Spillover slice from 099 (frontend `/evidence` list view). During slice 099 the frontend bound to the existing `GET /v1/evidence?control_id=<uuid>` endpoint at `internal/api/controldetail/handler.go`. That endpoint REQUIRES `control_id` — it was built for the per-control evidence panel (slice 064), not for a tenant-wide ledger view. Slice 099 ships a control-pill-driven UX as a result, with a "Pick a control to see its evidence ledger" prompt as the default empty state.

Per the slice 099 design call (D2) + the slice text itself ("preferred path is to extend the existing endpoint over adding a new one" + "If the GET /v1/evidence?... endpoint shape needs an extension, file as a backend follow-on slice rather than expanding this PR"), this slice files the backend extension:

1. Make `control_id` optional. When absent, return the tenant-wide ledger window.
2. Add `?kind=<evidence_kind>` filter (matches `evidence_records.evidence_kind`).
3. Add `?result=<pass|fail|na|inconclusive>` filter — requires surfacing `result` on the wire shape (today it's on `evidence_records.result` but `evidenceWire` doesn't include it).
4. Add `?source_actor_type=` / `?source_actor_id=` filter (matches the provenance JSONB).
5. Keep cursor + limit pagination semantics unchanged.

Once shipped, slice 099's page gets a follow-on PR that:

- Removes the "Pick a control to see its evidence ledger" prompt (default to tenant-wide view).
- Adds the four extra filter pills (Kind, Result, Source, Scope) per design doc §8.
- Adds the `result` column per design doc §7 (previously omitted per slice 099 P0-A1).

## Acceptance criteria

- [ ] AC-1: `GET /v1/evidence` (no query params) returns the tenant-wide evidence ledger window (last 30 days by default).
- [ ] AC-2: `GET /v1/evidence?control_id=<uuid>` continues to behave exactly as today (backwards compatible).
- [ ] AC-3: `GET /v1/evidence?kind=<evidence_kind>` narrows to records of one kind.
- [ ] AC-4: `GET /v1/evidence?result=<pass|fail|na|inconclusive>` narrows to records with the matching result.
- [ ] AC-5: `evidenceWire` gains a `result` field surfaced from `evidence_records.result`.
- [ ] AC-6: Multiple filters compose with AND semantics (`?control_id=&kind=&result=` narrows on all three).
- [ ] AC-7: Pagination semantics (`?cursor=`, `?limit=`, `next_cursor` response field) work for the tenant-wide window too.
- [ ] AC-8: Tenant isolation via RLS — confirmed via integration test that tenant A cannot see tenant B's evidence even when no filters narrow.
- [ ] AC-9: Unit + integration tests cover: control_id absent, control_id present, kind narrow, result narrow, composed filters, cursor pagination.
- [ ] AC-10: Coverage floor lift for `internal/api/controldetail` in the same PR (per repo testing discipline — ratchet is monotonically increasing).

## Constitutional invariants honored

- **Invariant 2 (ingestion/evaluation separated):** read-only over `evidence_records`; never writes.
- **Invariant 6 (tenant isolation):** RLS at the DB layer continues to enforce tenancy.

## Canvas references

- `Plans/canvas/04-evidence-engine.md`
- `internal/api/controldetail/handler.go` (`Evidence` handler — entry point)
- `internal/api/controldetail/store.go` (`EvidenceForControl` — query to extend)

## Dependencies

- **099** — frontend page that consumes this extension (in-review)
- **064** — original control-detail evidence endpoint (merged)
- **013** — evidence ledger write API + read endpoints (merged)

## Anti-criteria (P0)

- **P0-A1:** Does NOT break the existing `?control_id=` shape (backwards compatibility).
- **P0-A2:** Does NOT introduce a new endpoint (`/v1/evidence/list` etc.) — the extension lives on the existing route.
- **P0-A3:** Does NOT widen the wire shape with experimental fields — only `result` is added (and it's already on the DB column).
- **P0-A4:** Does NOT accept `tenant_id` from the client — tenant continues to be derived from the bearer.

## Notes

- The first follow-on slice (101 style: frontend "use the new shape" PR) will be filed against slice 099's page once this lands.
- Coverage-gate ratchet: the PR that extends the handler MUST also raise the per-package floor in `cmd/scripts/coverage-thresholds.json` to cover the new branches (per CLAUDE.md "Raising a coverage floor" rule).
