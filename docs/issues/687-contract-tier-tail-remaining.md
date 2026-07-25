# 687 — Contract-tier rollout: controls-detail + audit-workspace REMAINING tail

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — slice 412 lands the per-route seam pattern for the `controlstate` tail; the same Option-A seam pattern this slice extends is on `main`)
**Parent:** 412 (controls-detail `controlstate` tail) · 411 (first cut) · 409 (rollout origin)

## Narrative

Surfaced during slice 412, captured as a follow-up per continuous-batch policy.

Slice 412 drained the two highest-e2e-traffic, cheapest-seam tail routes
(`GET /v1/controls/{id}/state` + `/effectiveness`, served by
`internal/api/controlstate` over a single two-method seam). It **deferred the
rest of the tail** — most load-bearingly `GET /v1/controls/{id}/coverage`
(`internal/api/ucfcoverage`) — on a seam-cost judgment: unlike the clean
`*eval.Engine`-backed `controlstate` routes, `ucfcoverage.ControlCoverage` is a
transaction-orchestrating multi-query ASSEMBLER, and an honest Option-A seam
over it is a 6+-method interface (plus a fake `inTenantTx` plus the slice-256
coverage stores) to record one golden. That seam deserves its own deliberate
design, not a bolt-on. See slice 412 decisions log D5.

## What ships (when picked up)

Per-route Option-A read seams + provider recorders (no DB, no integration tag —
P0-409-1) + consumer asserts + drift proof for the remaining deferred routes.
Prioritize the ones the `/e2e/` suite still hand-mocks.

**Controls-detail remaining tail:**

- `GET /v1/controls/{id}/coverage` — `internal/api/ucfcoverage`. The
  load-bearing one (still e2e-mocked in `web/e2e/control-detail-tabs.spec.ts`).
  Design the seam deliberately — likely a narrow read-model method returning the
  assembled `(control, anchor, requirements)` triple the handler serializes,
  rather than a 6-method `dbx.Queries` mirror. Capture the anchored/unanchored
  and pinned/unpinned (`?framework_version=`) wire-shape forks as variants.
- `GET /v1/controls/{id}/attestations` / `attest-form` — the attestation
  handler (lower e2e traffic; include if the seam is cheap).
- `GET /v1/evidence?control_id=…` — the controldetail `Evidence` handler
  (tenant-wide ledger window, left on the concrete `*Store` by slices 411/412).
  Extending the existing `controlDetailReader` seam to the two evidence read
  methods (`EvidenceForControl` + `EvidencePaged` + `CountEvidenceForTenant`) is
  the judgment call here.

**Audit-workspace remaining tail:**

- `GET /v1/audit-periods/{id}` (single-period `Get`) + `/control-state` —
  `internal/api/auditperiods`.
- `GET /v1/audit-notes` + `/thread` — `internal/api/auditnotes`.
- populations / samples / walkthroughs read endpoints the
  `web/e2e/audit-workspace.spec.ts` + `audits-*.spec.ts` specs traverse —
  `internal/api/audit`.

## Acceptance criteria

- [ ] **AC-1.** Per-route read seams on the targeted remaining handlers;
      recorders on the unit surface (no DB; no integration tag — P0-409-1).
- [ ] **AC-2.** Goldens + consumer asserts (transform-aware where the BFF
      transforms; `toEqual` where it passes through) for the targeted routes;
      any further deferrals documented + spilled.
- [ ] **AC-3.** Drift sensitivity proven on ≥1 new endpoint.
- [ ] **AC-4.** Zero-new-gate (no `ci.yml` change; rides Go-unit + vitest).

## Dependencies

- **#412** — `merged` (when this lands). Drained the `controlstate` tail and
  established the deferral rationale for `ucfcoverage`.
- **#411** / **#409** / **ADR-0007** — the per-route seam pattern + the
  Option-A seam constraint + the origin tier.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 412 decisions log D5 (`docs/audit-log/412-contract-tier-tail-decisions.md`) — the `ucfcoverage` deferral rationale
- Slice 411 decisions log (`docs/audit-log/411-contract-tier-controls-audit-decisions.md`)
