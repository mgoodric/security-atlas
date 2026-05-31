# 394 — Teach the `/e2e/` `route.fulfill` mocks to load from the recorded contract goldens

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (unblocked by #409 — the dashboard-panel goldens landed; see Dependencies)

## Narrative

Surfaced during slice 392, captured per continuous-batch policy.

Slice 349 piloted, and slice 392 rolled out, the golden-file contract
tier (ADR-0007): a Go unit-surface recorder writes the real handler's
response body to a committed golden under `web/lib/contracts/`, and a
vitest consumer asserts the Next.js BFF against it. As of slice 392 the
tier covers `GET /v1/install-state`, `GET /v1/version`, `GET /v1/me`,
and `GET /v1/admin/demo/status`.

Slice 392's AC-3 asked whether the `/e2e/` Playwright suite's 57
`route.fulfill` upstream mocks (slice 334 finding P-1) should be taught
to load their bodies from those recorded goldens — so the e2e mocks
cannot drift from the provider's recorded truth. Slice 392 evaluated and
**deferred** the work (decisions log D5). This slice is that deferred
follow-on.

## Why it was deferred (slice 392 D5)

1. **Coverage mismatch.** The goldens cover 4 endpoints; the e2e mocks
   cover ~30+ distinct upstream routes across `web/e2e/*.spec.ts`. A
   golden-backed `fulfillFromGolden(variant)` helper only de-risks the
   handful of routes that have goldens; the rest still hand-write bodies.
   P-1's fragility is not materially reduced until golden coverage
   approaches the mock coverage.
2. **Per-test variation.** The e2e mocks frequently need error states,
   pagination, and empty-set bodies the happy-path goldens do not carry.
   A golden-backed helper still needs a hand-written-override escape
   hatch, so it does not eliminate hand-written bodies — it adds a second
   code path beside them.
3. **Premature abstraction risk.** Wiring it against 4 goldens risks a
   half-adopted pattern (some specs golden-backed, most not) that is
   harder to reason about than uniform hand-mocks.

## What ships (when unblocked)

- A `fulfillFromGolden(route, endpoint, variant)` Playwright helper
  (likely in `web/e2e/` test-utils) that reads a `web/lib/contracts/`
  golden and serves the recorded body via `route.fulfill`, with a
  documented hand-written-override path for the error/pagination/empty
  variants the goldens do not carry.
- Migration of the e2e specs whose routes HAVE goldens to use the helper.
- A note (or lint) discouraging new hand-written `route.fulfill` bodies
  for routes that have a golden.

## Acceptance criteria

- [ ] **AC-1.** A Playwright `route.fulfill` helper loads bodies from the
      `web/lib/contracts/` goldens.
- [ ] **AC-2.** Every e2e spec whose upstream route has a golden uses the
      helper (no hand-written body for a golden-covered route).
- [ ] **AC-3.** The error/pagination/empty-set variation escape hatch is
      documented and tested.
- [ ] **AC-4.** Stays zero-new-gate (ADR-0007) — no new CI job; rides the
      existing Playwright surface.

## Dependencies

- **#392** (contract-tier rollout) — establishes the multi-endpoint
  goldens this helper would read.
- **Broader golden coverage** — RESOLVED for the dashboard view by #409
  (merged 2026-05-30). The golden tier now spans the high-traffic
  dashboard-panel routes the e2e suite traverses: `/v1/frameworks/posture`,
  `/v1/activity`, `/v1/upcoming`, `/v1/evidence/freshness`,
  `/v1/controls/drift` (all via #409, Option-A read seams) plus `/v1/me`,
  `/v1/version`, `/v1/install-state`, `/v1/admin/demo/status` (from #392).
  That is 9 endpoints, and every dashboard route the e2e suite mocks now
  has a golden — so a `fulfillFromGolden` helper for the dashboard view is
  no longer premature (D5 reason 1 cleared; see
  `docs/audit-log/409-contract-tier-rollout-dashboard-decisions.md` D5).
  The still-hand-mocked routes (`/v1/risks`, `/v1/controls/*`, `/v1/board`,
  `/v1/policies`) are exactly the per-test-variation escape-hatch case AC-3
  anticipates and do NOT block building the helper for the golden-backed
  routes; their goldens are tracked as #410 (risks) + #411 (controls/audit).
- **#409** (contract-tier rollout: dashboard routes) — the concrete
  unblocker, **merged 2026-05-30**. It recorded the dashboard-panel
  goldens (resolving the ADR-0007 DB-seam constraint per-endpoint via
  unexported read seams) and flipped THIS slice `blocked` → `ready`.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 349 (`docs/issues/349-contract-test-tier-evaluation.md`) — pilot
- Slice 392 (`docs/issues/392-contract-test-tier-rollout.md`) — rollout + the D5 deferral
- Slice 334 P-1 (`docs/audits/334-test-framework-review.md`) — the mock-density finding
