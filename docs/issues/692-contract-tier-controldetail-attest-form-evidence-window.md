# 692 — Contract-tier rollout: controldetail attest-form + per-control Evidence ledger window (the two dbx-coupled reads slice 690 deferred)

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — the slice-411/412/687/689/690 per-route
Option-A seam + recorder + transform-aware-vs-field-contract-assert pattern is
on `main`)
**Parent:** 690 (audit-workspace read-tail remainder) · 689 · 687 · 412 · 411 ·
409 (rollout origin)

## Narrative

Surfaced during slice 690, captured as a follow-up per continuous-batch policy.

Slice 690 drained the three audit-workspace LIST reads that extend an existing
689 per-route seam and are pure-Go to record (`GET /v1/samples/{id}/annotations`
in `internal/api/audit`, `GET /v1/walkthroughs` list in
`internal/api/walkthroughs`, `GET /v1/audit-notes` legacy list in
`internal/api/auditnotes` — all field-contract pins, slice 687 D3). It
**deferred the two control-detail reads** that each carry their own distinct,
heavier seam shape: both read through Postgres-coupled `dbx`-row types (not the
clean domain structs the audit-workspace handlers return), so each is its own
recorder-fixture story rather than a one-line extension of an existing seam.
See slice 690 decisions log D3.

## What ships (when picked up)

Per-route Option-A read seams + provider recorders (no DB, no integration tag —
P0-409-1) + consumer asserts + drift proof for the two deferred control-detail
reads. Both are field-contract pins today (slice 687 D3) unless a verbatim GET
BFF is confirmed.

- **`GET /v1/controls/{id}/attest-form`** — `internal/api/controls/attest.go`
  `AttestForm`. The handler reads the control via `h.loadControl` →
  `dbx.GetControlByIDRow` (a `pgtype.UUID`-coupled sqlc row) inside a
  tenant-GUC read tx, then assembles `attestFormResponse` (a schema descriptor:
  `manual_evidence_schema` + platform schema kind/version/requires +
  `caller_can_attest`). The seam is its own shape: it returns a
  `dbx.GetControlByIDRow`, and the recorder must build that row fixture
  (including the JSONB `manual_evidence_schema`). Note the GET surface is
  `attest-form` only — there is NO `GET /v1/controls/{id}/attestations`
  (`attestations` is POST `Submit`). Pin the manual + non-manual (400) and the
  `caller_can_attest` true/false branches.

- **`GET /v1/evidence?control_id=…`** — `internal/api/controldetail` `Evidence`,
  the per-control ledger window. Left on the concrete `*Store` by slices
  411/412/689/690 — its own keyset-pagination + `CountEvidenceForTenant`
  two-method seam (`EvidenceForControl(ctx, controlID, evidencePage)` returns
  `[]dbx.ListEvidenceForControlPagedRow`; the unexported `evidencePage` cursor
  struct + `next_cursor` keyset encoding are part of the seam). Pin the
  per-control wire shape (`evidenceWire`: evidence_id / evidence_kind (nullable)
  / observed_at / source (JSONB) / content_hash / scope_cell (nullable) /
  result) + the `{control_id, evidence, count, total, next_cursor}` envelope.
  Decide whether to seam just the per-control branch or also the tenant-wide
  branch (slice 106) — the e2e suite does not traverse the tenant-wide window.

## Acceptance criteria

- [ ] **AC-1.** Per-route read seams on `attest.go` (AttestForm) + the
      controldetail Evidence per-control path; recorders on the unit surface
      (no DB; no integration tag — P0-409-1). Public constructor signatures
      unchanged (P0-409-2: `NewAttestHandler` + `controldetail.New`).
- [ ] **AC-2.** Goldens + consumer asserts (field-contract where no BFF
      consumes the route yet — slice 687 D3; `toEqual` if a verbatim
      passthrough GET BFF is confirmed). Any further deferrals documented +
      spilled.
- [ ] **AC-3.** Drift sensitivity proven on ≥1 new endpoint.
- [ ] **AC-4.** Zero-new-gate (no `ci.yml` change; rides Go-unit + vitest).

## Dependencies

- **#690** — `merged` (when this lands). Drained the three audit-workspace LIST
  reads + the extends-existing-seam disposition this slice's deferred routes did
  NOT fit.
- **#689** / **#687** / **#411** / **#412** / **#409** / **ADR-0007** — the
  per-route seam pattern + the Option-A seam constraint + the
  field-contract-without-BFF disposition + the origin tier.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 690 decisions log
  (`docs/audit-log/690-contract-tier-audit-remainder-decisions.md`) — D3
  (the two dbx-coupled deferrals), D1 (route scope)
- Slice 687 decisions log
  (`docs/audit-log/687-contract-tier-tail-remaining-decisions.md`) — D3
  (no-BFF field-contract disposition)
