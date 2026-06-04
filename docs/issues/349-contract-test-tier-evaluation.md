# 349 — Evaluate adding a contract-test tier for BFF↔atlas wire shape

**Cluster:** Quality
**Estimate:** 2-3d (evaluation + pilot)
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 333's QA strategy audit (`docs/audits/333-qa-strategy-gap-analysis.md`)
finding Q-1: the four-surface test gate has no tier that pins the
HTTP wire shape between the Next.js BFF and the atlas Go API. The
Playwright `/e2e/` suite mocks the upstream atlas API in 57
`route.fulfill` calls (slice 334 P-1); the Go integration tier
exercises atlas in isolation but not the BFF's consumption of it.
A BFF that drifts from atlas's actual response shape is caught only
when a Playwright spec happens to assert on a mock that matches the
real shape — silent drift between mock and reality is the failure
mode.

The audit proposes a **contract-test tier** between Go integration
and Playwright e2e. This slice evaluates whether to ship one, in
what form, and at what cost.

### Evaluation surface

1. **Golden-file approach.** atlas integration tests already make
   real HTTP calls; record the response bodies as `testdata/*.json`
   golden files; assert BFF code under vitest against the same golden
   files. Pro: low infrastructure cost; reuses existing tests. Con:
   golden-file drift is its own discipline; updates need a `-update`
   flag pattern.

2. **Schemathesis / openapi-based.** atlas already publishes an
   OpenAPI spec (slice 339 covered drift detection). A schemathesis
   step would generate request/response contract tests from the spec
   and run them against both atlas and the BFF's expectations. Pro:
   spec-driven; surfaces schema drift in CI. Con: requires the
   OpenAPI spec to be complete and accurate (slice 339 watches drift,
   not completeness).

3. **gRPC contract testing via buf breaking.** `proto/` schemas
   already exist (`evidence.proto`, `connectors.proto`, `oscal.proto`,
   `admin/credentials.proto`). `buf breaking` could catch incompatible
   changes; consumer-side tests are still needed for HTTP-only
   surfaces. Pro: gRPC contracts are explicit. Con: doesn't cover
   HTTP-only surfaces (most of the BFF's consumption is REST).

4. **Do nothing; rely on slice 351's e2e critical flow audit + Q-10's
   ui-honesty promotion.** Pro: avoids a new tier; the `e2e-audit/`
   harness with real services is a partial substitute. Con: doesn't
   address per-endpoint wire-shape drift, only end-to-end happy paths.

### What ships in this slice

- A 2-page evaluation document at
  `docs/adr/00NN-contract-test-tier.md` walking through the four
  options against the project's risk profile and operational cost.
- A pilot: pick the highest-value endpoint pair (recommended:
  `/api/admin/install-state` + `/api/admin/demo/status` since both
  have recent bug history) and prototype option (1) or (2) for that
  pair only.
- A decision: which option to roll out broadly, OR "no contract tier;
  invest in slice 351 + Q-10 path instead."

## Threat model

QA-evaluation slice; no runtime code changes. STRIDE clean.

## Acceptance criteria

- [ ] **AC-1.** Evaluation doc at `docs/adr/00NN-contract-test-tier.md`
      with all four options scored against (a) build cost, (b)
      maintenance cost, (c) drift-detection sensitivity, (d) fit with
      slice 069 ratchet contract.
- [ ] **AC-2.** Pilot for one endpoint pair, regardless of which
      option is selected — proves the chosen approach works.
- [ ] **AC-3.** Decisions log at
      `docs/audit-log/349-contract-test-tier-decisions.md` per
      JUDGMENT-slice convention.
- [ ] **AC-4.** If "ship the tier" is the decision, file a follow-on
      slice for broad rollout (do NOT roll out in this slice — keep
      the pilot tight).
- [ ] **AC-5.** If "no tier" is the decision, document the rationale
      and the compensating strategy (slice 351 + Q-10).
- [ ] **AC-6.** Cross-references slice 333 Q-1 and slice 334 P-1.

## Anti-criteria

- **P0-1.** Does NOT roll out a tier across all endpoints in this
  slice. Pilot only.
- **P0-2.** Does NOT pick option (3) — gRPC `buf breaking` — without
  separately addressing HTTP-only surfaces.
- **P0-3.** Does NOT remove the existing `/e2e/` mocked-upstream
  suite. The merged gate keeps its current shape during evaluation.

## Dependencies

- **#333** (QA strategy audit) — `merged`. Defines Q-1.
- **#334** (test framework review) — `merged`. Defines P-1.
- **#339** (OpenAPI OAuth endpoint spec drift) — `merged`. Establishes
  the OpenAPI spec drift detection (relevant to option 2).

## Notes for the implementing agent

This is the strategic call slice 333 anticipated. Resist scope creep:
the slice ships an evaluation + pilot + decision, NOT a rolled-out
tier. The decision can be "no tier" and that is a successful slice
outcome — what's not acceptable is shipping the tier without scoping
the broader cost.
