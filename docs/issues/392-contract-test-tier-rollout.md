# 392 — Roll out the golden-file contract-test tier to high-traffic BFF↔atlas endpoint pairs

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 349, captured per continuous-batch policy.

Slice 349 evaluated and piloted a golden-file contract-test tier for the
BFF↔atlas HTTP wire shape (decision: ADR-0007, **PILOT then ADOPT**).
The pilot proved the mechanism on one endpoint pair
(`GET /v1/install-state`): a provider-side Go unit test records the real
handler body to a committed golden; a consumer-side vitest test asserts
the Next.js BFF against that golden; a field rename fails both halves.

Slice 349 was deliberately scoped to the pilot only (slice 349 P0-1).
This slice extends the proven mechanism to the remaining high-traffic
endpoint pairs and evaluates whether the `/e2e/` `route.fulfill` mocks
(slice 334 P-1; 57 calls) can be taught to load from the goldens —
retiring the mock-vs-reality fragility at its root.

## What ships

- Extend the golden-file contract tier (the pattern established at
  `internal/api/install_state_contract_test.go` +
  `web/lib/contracts/install-state.{golden.json,contract.test.ts}`) to
  the next tranche of high-traffic / bug-prone BFF↔atlas pairs.
  Recommended first targets:
  - `GET /v1/admin/demo/status` (`{enabled: bool}`) — slice 349's
    secondary suggestion; trivial shape, fast win.
  - `GET /v1/me` — the auth/identity surface; high consumer coupling.
  - `GET /v1/version` — slice 072 BFF pattern; cheap.
  - `GET /v1/metrics` (dashboard) — high traffic, non-trivial shape.
- Evaluate (and, if cheap, implement) a `route.fulfill` helper that
  loads the recorded golden so the `/e2e/` mocks cannot drift from the
  recorded provider truth.

## Anti-criteria

- **P0-1.** Does NOT introduce Pact / schemathesis / a new CI job. The
  tier rides the existing Go-unit + vitest surfaces (ADR-0007 decision).
- **P0-2.** Does NOT touch `.github/workflows/ci.yml` for a new job
  (the tier needs none). If a CI change is later argued for, it is its
  own slice.
- **P0-3.** Keeps each golden small and the `-update` regeneration flow
  intact; no hand-edited variant bodies.

## Acceptance criteria

- [ ] **AC-1.** At least 3 additional endpoint pairs covered by the
      golden-file contract tier, following the slice-349 pattern.
- [ ] **AC-2.** Each new contract test proven drift-sensitive (a field
      rename fails the test) — documented in the PR.
- [ ] **AC-3.** Decision recorded on whether the `/e2e/` mocks load from
      the goldens (implement if cheap; defer with rationale if not).
- [ ] **AC-4.** Cross-references ADR-0007 and slice 349.

## Dependencies

- **#349** (contract-test-tier evaluation + pilot) — establishes the
  pattern and ADR-0007. Must be merged first.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 349 (`docs/issues/349-contract-test-tier-evaluation.md`)
- Slice 334 P-1, slice 333 Q-1
