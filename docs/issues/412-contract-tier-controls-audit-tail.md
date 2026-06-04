# 412 — Contract-tier rollout: controls-detail + audit-workspace LONG TAIL

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — slice 411 landed the per-route seam pattern for both families)
**Parent:** 411 (contract-tier controls-detail + audit-workspace first cut) · 409 (rollout origin)

## Narrative

Surfaced during slice 411, captured per continuous-batch policy.

Slice 411 landed the golden-file contract tier (ADR-0007) on the
**highest-traffic** controls-detail and audit-workspace routes the `/e2e/`
suite hand-mocks: the three control-detail tab reads
(`GET /v1/controls/{id}/{policies,risks,history}`, served by
`internal/api/controldetail`) and the audit-workspace period index
(`GET /v1/audit-periods`, served by `internal/api/auditperiods`). Per the
slice-411 spec's scope discipline ("these families are large and likely
split into two slices"), it **deferred the long tail** to keep the first
cut a coherent bounded slice. This slice drains that tail.

See `docs/audit-log/411-contract-tier-controls-audit-decisions.md` D5 for
the per-route defer rationale.

## What ships (when picked up)

Per-route Option-A read seams + provider recorders (no DB, no integration
tag — P0-409-1) + consumer asserts + drift proof for the deferred routes.
Prioritize the ones the `/e2e/` suite still hand-mocks after slice 411.

**Controls-detail tail** (separate packages from controldetail — each needs
its own narrow seam):

- `GET /v1/controls/{id}/coverage` — `internal/api/ucfcoverage` (the UCF
  graph-traversal handler; the e2e suite route-fulfills
  `/api/controls/{id}/coverage`). Likely transform-check the BFF first.
- `GET /v1/controls/{id}/state` — `internal/api/controlstate` (the e2e
  suite route-fulfills `/api/controls/{id}/state`).
- `GET /v1/controls/{id}/effectiveness` — `internal/api/controlstate`
  (same package as state; a two-method seam may cover both).
- `GET /v1/controls/{id}/attestations` / `attest-form` — the attestation
  handler. Lower e2e traffic; include only if the seam is cheap.
- `GET /v1/evidence?control_id=…` — the controldetail `Evidence` handler,
  explicitly left on the concrete `*Store` by slice 411 (tenant-wide
  ledger window, not part of the control-detail tab cluster). The seam
  already exists (`controlDetailReader`); extending it to cover the two
  evidence read methods (`EvidenceForControl` + `EvidencePaged` +
  `CountEvidenceForTenant`) is a follow-on judgment call.

**Audit-workspace tail** (the `/v1/audit/*` populations/samples/
walkthroughs/notes families — confirm package locations via
`internal/api/audit*`):

- `GET /v1/audit-periods/{id}` (single period get) + `/control-state`.
- `GET /v1/audit-notes` + `/thread`.
- populations / samples / walkthroughs read endpoints the
  `/e2e/audits-*.spec.ts` specs traverse.

## Acceptance criteria

- [ ] **AC-1.** Per-route read seams on the targeted tail handlers;
      recorders on the unit surface (no DB; no integration tag — P0-409-1).
- [ ] **AC-2.** Goldens + consumer asserts (transform-aware where the BFF
      transforms; `toEqual` where it passes through) for the targeted
      routes; any further deferrals documented + spilled.
- [ ] **AC-3.** Drift sensitivity proven on ≥1 new endpoint.
- [ ] **AC-4.** Zero-new-gate (no `ci.yml` change; rides Go-unit + vitest).

## Dependencies

- **#411** — `merged` (when this lands). Established the per-route seam +
  recorder + transform-aware-vs-passthrough-assert pattern for both
  families.
- **#409** / **ADR-0007** — the origin tier + the Option-A seam constraint.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 411 decisions log (`docs/audit-log/411-contract-tier-controls-audit-decisions.md`)
- Slice 409 decisions log D6 (the original deferral of these families)
