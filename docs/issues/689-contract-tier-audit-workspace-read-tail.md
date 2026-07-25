# 689 — Contract-tier rollout: audit-workspace read tail (populations / samples / walkthroughs / notes + attestations + Evidence window)

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — slice 687 lands the `ucfcoverage` thin
read-model seam + the `auditperiods` two-method `periodReader` seam; the same
Option-A per-route seam pattern this slice extends is on `main` via slices
411/412/687)
**Parent:** 687 (ucfcoverage + audit-period reads) · 412 (controlstate tail) ·
411 (first cut) · 409 (rollout origin)

## Narrative

Surfaced during slice 687, captured as a follow-up per continuous-batch policy.

Slice 687 drained the load-bearing `ucfcoverage` `GET /v1/controls/{id}/coverage`
route (the one slice 412 D5 deferred on seam-cost) plus the `auditperiods`
single-period `Get` + `/control-state` reads. It **deferred the rest of the
audit-workspace read tail** on the slice-411/412 bounded-cut discipline (a clean
coherent slice + a spillover beats an overreaching one). See slice 687 decisions
log D4.

## What ships (when picked up)

Per-route Option-A read seams + provider recorders (no DB, no integration tag —
P0-409-1) + consumer asserts + drift proof for the remaining deferred routes.
Prioritize the ones with a real verbatim-passthrough GET BFF and the ones the
`/e2e/` suite still hand-mocks.

**Audit-workspace remaining reads:**

- `GET /v1/populations` (list) + `GET /v1/samples` (list) — the
  `internal/api/audit` package. NOTE: the populations/samples BFFs mix POST
  (create) with GET (list); inventory the READ subset first
  (`web/app/api/audit/populations/route.ts` is a POST — find the list GET).
- `GET /v1/walkthroughs` (list) + single `Get` — `internal/api/walkthroughs`.
- `GET /v1/audit-notes` (list) + `/thread` — `internal/api/auditnotes`. The
  `/thread` route has a real verbatim-passthrough BFF
  (`web/app/api/audit/audit-notes/thread/route.ts`).

**Controls-detail remaining tail:**

- `GET /v1/controls/{id}/attestations` / `attest-form` — the attestation handler
  at `internal/api/controls/attest.go` (lower e2e traffic; include if the seam
  is cheap).
- `GET /v1/evidence?control_id=…` — the controldetail `Evidence` handler
  (tenant-wide ledger window), left on the concrete `*Store` by slices 411/412.

**Audit-period passthrough half (paired with slice 687):**

- When a single-period / audit-sampling BFF lands that consumes
  `GET /v1/audit-periods/{id}` or `/control-state`, add the passthrough-DRIVE
  consumer half to slice 687's field-contract goldens
  (`web/lib/contracts/audit-period-get.golden.json` +
  `audit-period-control-state.golden.json` are already in place; only the
  BFF-drive vitest is missing — see slice 687 D3).

## Acceptance criteria

- [ ] **AC-1.** Per-route read seams on the targeted remaining handlers;
      recorders on the unit surface (no DB; no integration tag — P0-409-1).
- [ ] **AC-2.** Goldens + consumer asserts (transform-aware where the BFF
      transforms; `toEqual` where it passes through; field-contract where no BFF
      consumes the route yet — slice 687 D3) for the targeted routes; any
      further deferrals documented + spilled.
- [ ] **AC-3.** Drift sensitivity proven on ≥1 new endpoint.
- [ ] **AC-4.** Zero-new-gate (no `ci.yml` change; rides Go-unit + vitest).

## Dependencies

- **#687** — `merged` (when this lands). Drained the `ucfcoverage` /coverage
  seam + the auditperiods reads and established the thin-read-model-seam pattern
  for tx-orchestrating assemblers + the field-contract-without-BFF disposition.
- **#411** / **#412** / **#409** / **ADR-0007** — the per-route seam pattern +
  the Option-A seam constraint + the origin tier.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 687 decisions log (`docs/audit-log/687-contract-tier-tail-remaining-decisions.md`)
  — D3 (no-BFF field-contract disposition), D4 (route scoping + the deferred list)
- Slice 412 decisions log D5 (`docs/audit-log/412-contract-tier-tail-decisions.md`)
