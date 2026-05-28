# 351 — e2e critical multi-tenant flow gap audit + spec fill

**Cluster:** Quality / e2e
**Estimate:** 2-3d
**Type:** AFK
**Status:** `ready`

## Narrative

Slice 333's QA strategy audit
(`docs/audits/333-qa-strategy-gap-analysis.md`) finding Q-9: critical
multi-tenant flows are unrepresented or skipped in the merged
Playwright e2e gate. Spot-check:

| Flow                              | e2e spec status                                          |
| --------------------------------- | -------------------------------------------------------- |
| tenant-switch (multi-tenant user) | **no spec found** — `grep tenant-switch web/e2e/*` empty |
| super-admin operations            | `super-admins.spec.ts` exists; coverage TBD              |
| auth-open-redirect                | `test.skip` — quarantined                                |
| evidence push CLI → UI display    | `evidence-list.spec.ts` (UI side only)                   |
| board-pack export end-to-end      | `board-pack-detail.spec.ts` (UI side only)               |
| bff-cookie-production-build       | `test.skip` — quarantined                                |
| logo-render-production-build      | `test.skip` — quarantined                                |
| questionnaires                    | `test.skip` — quarantined                                |
| risks-create                      | `test.skip` — quarantined                                |

This slice does two things:

1. **Audit pass.** Enumerate the project's critical multi-tenant
   flows against the v1 success test ("does the user run their next
   SOC 2 audit out of security-atlas?"). Produce a coverage matrix:
   flow × current spec status × priority.

2. **Spec fill — P0 only.** Author specs for the P0 missing flows
   (tenant-switch is the canonical example). Triage the 8
   `test.skip` quarantines: each becomes (a) un-skipped with a fix,
   (b) un-skipped with a justification comment + open spillover slice,
   or (c) deleted if obsolete.

### Out of scope

- P1 / P2 specs (file follow-up slices).
- Fixing the underlying bugs that caused the `test.skip` quarantines
  (file individual slices if needed).
- Promoting the `e2e-audit/` ui-honesty harness — that's Q-10's
  job (slice 353 round-1 path-planning).

## Threat model

This slice **adds** test specs. The audit-pass portion produces a
coverage matrix that names which security flows are tested vs
untested. STRIDE pass:

- **I (information disclosure):** The coverage matrix names which
  multi-tenant flows are untested — a roadmap to find an
  untested-multi-tenant-flow attack surface. **Mitigation:** the
  matrix lives in the repo; access is the repo access-control surface;
  do NOT publish to a public mirror or share externally. Same
  discipline as slice 333 (information disclosure mitigation).
- Others: CLEAN.

## Acceptance criteria

- [ ] **AC-1.** Coverage matrix at
      `docs/audits/351-e2e-critical-flow-coverage-matrix.md`. Lists
      every critical multi-tenant flow against current spec status
      and priority.
- [ ] **AC-2.** Tenant-switch spec authored:
      `web/e2e/tenant-switch.spec.ts`. Covers (a) user with N tenants
      sees the switcher, (b) switching changes the tenant context for
      subsequent requests, (c) switching does NOT leak data across
      tenants (assert on a known-tenant-A row not visible in tenant B
      view).
- [ ] **AC-3.** Evidence-push-end-to-end spec authored:
      `web/e2e/evidence-push-e2e.spec.ts`. Covers CLI push → atlas
      ingest → BFF read → UI display.
- [ ] **AC-4.** Each of the 8 `test.skip` quarantines is triaged
      per the (a) / (b) / (c) classification above. Document the
      triage outcome in the coverage matrix.
- [ ] **AC-5.** `npx playwright test` against a locally-running
      docker-compose stack passes the new specs.
- [ ] **AC-6.** CI gates the new specs (no change to the existing
      `frontend-playwright` job other than the new spec files).
- [ ] **AC-7.** Cross-references slice 333 Q-9 and slice 334 P-4.

## Anti-criteria

- **P0-1.** Does NOT ship a P1/P2 spec fill — file follow-ups.
- **P0-2.** Does NOT fix the bug underlying any `test.skip`
  quarantine in this slice; if a fix is needed, file a separate
  slice. This slice's job is triage + P0 spec fill.
- **P0-3.** Does NOT promote `e2e-audit/` to merge-blocking — that's
  a separate strategic question (Q-10).
- **P0-4.** Does NOT add mocks for new specs beyond the existing
  `e2e/` suite convention. New specs follow the established pattern
  (`route.fulfill` for atlas API) — this slice is not the right
  surface to change the hybrid-mock pattern.

## Dependencies

- **#333** (QA strategy audit) — `merged`. Defines Q-9.
- **#334** (test framework review) — `merged`. Defines P-4 (skip
  quarantines).
- **#201** (Playwright JWT migration) — `merged`. The fixture
  pattern the new specs build on.

## Notes for the implementing agent

The audit-pass portion is the load-bearing deliverable; the spec
fill is the proof. Resist scope creep on the fixes (P0-2). If a
quarantined spec has been broken for 6 months and the underlying
bug is unfixable in <30 min, the right outcome is (b) — un-skip,
write the justification, file a slice. Quarantined specs that
nobody can name a reason for should become (c) — deleted.

The tenant-switch spec is P0 because it's the multi-tenant
security-critical flow with zero coverage. Cross-tenant leak
assertions are the highest-value part of that spec.
