# 690 — Contract-tier rollout: audit-workspace read-tail remainder (sample-annotations / list reads / attestations / Evidence window / audit-period passthrough)

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — the slice-411/412/687/689 per-route Option-A
seam + recorder + transform-aware-vs-passthrough-assert pattern is on `main`;
slice 689 lands the four single-resource audit-workspace reads + the three new
package seams this slice extends)
**Parent:** 689 (audit-workspace single-resource reads) · 687 (ucfcoverage +
audit-period reads) · 412 (controlstate tail) · 411 (first cut) · 409 (rollout
origin)

## Narrative

Surfaced during slice 689, captured as a follow-up per continuous-batch policy.

Slice 689 drained the four load-bearing audit-workspace single-resource reads
that each have a SHIPPED verbatim-passthrough GET BFF
(`GET /v1/populations/{id}`, `GET /v1/samples/{id}`, `GET /v1/walkthroughs/{id}`,
`GET /v1/audit-notes/thread`) and added the per-route Option-A read seams to
`internal/api/audit`, `internal/api/walkthroughs`, and `internal/api/auditnotes`.
It **deferred the rest of the audit-workspace read tail** on the
slice-411/412/687 bounded-cut discipline (a clean coherent slice + a spillover
beats an overreaching one). See slice 689 decisions log D4.

## What ships (when picked up)

Per-route Option-A read seams + provider recorders (no DB, no integration tag —
P0-409-1) + consumer asserts + drift proof for the remaining deferred routes.
For routes that have a real verbatim-passthrough GET BFF, the consumer half is a
full passthrough drive (`toEqual`); for routes with NO BFF consumer today, the
consumer half is a field-contract pin on the recorded provider golden (the
slice-687 D3 disposition) — documented + spilled if a further cut is needed.

**Audit-workspace remaining reads:**

- `GET /v1/samples/{id}/annotations` (the auditor-decision list on a sample) —
  `internal/api/audit` `ListAnnotations`. No verbatim-passthrough BFF today (read
  by the sample-detail component, not a thin passthrough) → field-contract pin
  unless a passthrough BFF lands.
- `GET /v1/walkthroughs` (list) — `internal/api/walkthroughs` `List`. The list
  BFF (`web/app/api/audit/walkthroughs/route.ts`) is POST-only today; no GET
  consumer → field-contract pin (slice 687 D3) until a list GET BFF lands.
- `GET /v1/audit-notes` (legacy author-scoped list) — `internal/api/auditnotes`
  `List`. No GET BFF (the workspace reads `/thread`) → field-contract pin.

**Controls-detail remaining tail:**

- `GET /v1/controls/{id}/attestations` / `attest-form` — the attestation handler
  at `internal/api/controls/attest.go` (lower e2e traffic; `attest-form`
  assembles a schema descriptor — its own seam shape).
- `GET /v1/evidence?control_id=…` — the controldetail per-control `Evidence`
  ledger window, left on the concrete `*Store` by slices 411/412/689 (its own
  keyset-pagination + `CountEvidenceForTenant` two-method seam; not part of the
  control-detail tab cluster the e2e suite traverses).

**Audit-period passthrough half (paired with slice 687):**

- When a single-period / audit-sampling BFF lands that consumes
  `GET /v1/audit-periods/{id}` or `/control-state`, add the passthrough-DRIVE
  consumer half to slice 687's field-contract goldens
  (`web/lib/contracts/audit-period-get.golden.json` +
  `audit-period-control-state.golden.json` are already in place; only the
  BFF-drive vitest is missing — see slice 687 D3 + slice 689 D4).

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

- **#689** — `merged` (when this lands). Drained the four single-resource
  audit-workspace reads + added the `audit` / `walkthroughs` / `auditnotes`
  per-route seams this slice extends.
- **#687** / **#411** / **#412** / **#409** / **ADR-0007** — the per-route seam
  pattern + the Option-A seam constraint + the field-contract-without-BFF
  disposition + the origin tier.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 689 decisions log (`docs/audit-log/689-contract-tier-audit-workspace-decisions.md`)
  — D2 (the populations/samples "list" spec mischaracterization), D4 (the
  deferred list)
- Slice 687 decisions log (`docs/audit-log/687-contract-tier-tail-remaining-decisions.md`)
  — D3 (no-BFF field-contract disposition)
