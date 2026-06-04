# 424 — End-to-end test for the vendor-review workflow

**Cluster:** Quality
**Estimate:** 1-2d (M)
**Type:** AFK
**Status:** `ready`
**Priority:** P2

## Narrative

**WHY.** Vendor reviews are an explicit v1-binary criterion — the solo
security leader runs "vendor reviews" alone as part of the program the
product must wholly replace (CLAUDE.md persona). Yet there is **no
vendor-review e2e**. The closest spec,
`web/e2e/audit-periods-vendors-export.spec.ts`, tests only the _export_
path's email-masking — it never drives the review workflow itself
(vendor list → open a review → questionnaire link / status transition).
A v1-binary user surface with no full-stack e2e is a gap in the binary
success test: if the vendor-review flow regresses, nothing browser-level
catches it.

**WHAT.** A Playwright spec that drives the vendor-review workflow: the
vendor list → open a vendor review → exercise the questionnaire link
and/or a review-status transition. Use a mocked BFF where goldens exist,
reusing the slice-394 `fulfillFromGolden` recorder pattern
(`web/e2e/test-utils/fulfill-from-golden.ts`) so the spec is
deterministic and does not depend on live review state the bring-up
cannot seed.

**SCOPE DISCIPLINE.** One workflow path — list → open → one meaningful
transition (status change or questionnaire link). It does NOT re-test the
export email-masking (already covered by the existing spec) and does NOT
add a vendor feature. If a status transition the spec wants to drive has
no golden, the spec records one via `fulfillFromGolden` or the
transition is deferred + spilled rather than driven against unseeded
live state.

## Threat model

**S — Spoofing.** Vendor-review pages must require an authenticated
operator session.

- Mitigation: the spec authenticates via the standard e2e JWT bearer
  fixture (slice 201); only the authenticated path is driven.

**T — Tampering.** A status transition mutates review state.

- Mitigation: with a mocked BFF (`fulfillFromGolden`), the transition is
  asserted against a recorded contract — the spec verifies the
  client→BFF request shape, not unconstrained live mutation. Where the
  spec drives a real transition, it runs in the single seeded tenant.

**R — Repudiation.** Review status changes should be auditable.

- Mitigation: no new audit surface; the e2e proves the operator-visible
  transition. Audit-row assertions stay at the Go tier.

**I — Information disclosure (HEADLINE — tenant isolation).** Vendor data
is tenant-scoped; a review page must not surface another tenant's
vendors or contacts.

- Mitigation: the spec runs as one tenant's operator and asserts only
  that tenant's vendors render. The negative cross-tenant case is
  asserted at the Go integration tier (RLS); the e2e confirms the
  in-scope positive path. The existing export spec already covers
  contact email-masking — this spec does not regress it.

**D — Denial of service.** A large vendor list could render unbounded.

- Mitigation: out of scope — the spec drives seed-sized data;
  pagination caps are a handler concern.

**E — Elevation of privilege.** The review workflow must be gated to
operators with vendor-review permission, not read-only viewers.

- Mitigation: the spec uses an operator-role bearer; the role gate is
  handler-enforced (Go-tested). The e2e confirms the operator path.

**Verdict:** `has-mitigations`. Tenant-isolation on vendor data is the
headline; the e2e drives the in-scope positive path with a mocked
contract and defers the cross-tenant negative to the Go tier.

## Acceptance criteria

- [ ] **AC-1 (test).** A new Playwright spec
      (e.g. `web/e2e/vendor-review-workflow.spec.ts`) navigates to the
      vendor list and asserts the seeded tenant's vendors render.
- [ ] **AC-2 (test).** The spec opens a vendor review (vendor row → review
      detail) and asserts the review surface renders (status, contact,
      questionnaire link as applicable).
- [ ] **AC-3 (test).** The spec exercises ONE meaningful interaction: a
      review-status transition AND/OR following the questionnaire link,
      asserting the resulting UI state.
- [ ] **AC-4 (test).** Where the BFF is mocked, the spec uses the
      slice-394 `fulfillFromGolden` recorder
      (`web/e2e/test-utils/fulfill-from-golden.ts`); any new golden is
      committed and the request-shape assertion is transform-aware.
- [ ] **AC-5 (test).** The spec asserts only the single seeded tenant's
      vendor data renders (no cross-tenant leakage on the positive path).
- [ ] **AC-6 (test).** The spec runs against the docker-compose bring-up
      seed data — no precondition the bootstrap cannot provide
      (`web/e2e/README.md`); a seed gap is a spillover, not a workaround.
- [ ] **AC-7.** The spec is enrolled in the `Frontend · Playwright e2e`
      CI job and passes; failed runs upload the HTML report + traces.
- [ ] **AC-8.** The existing `web/e2e/audit-periods-vendors-export.spec.ts`
      email-masking assertions are left intact (orthogonal coverage).

## Constitutional invariants honored

- **Tenant isolation enforced at the DB layer (invariant #6).** The spec
  asserts single-tenant vendor rendering; RLS is the enforcement, the
  e2e the operator-visible confirmation.
- **Manual evidence is first-class (invariant #9).** The questionnaire /
  review surface renders the same way regardless of automation.
- **Testing discipline (CLAUDE.md).** Playwright is the de facto
  component-test tier (slice 353 Q-3); this spec catches review-flow
  regressions there.

## Canvas references

- `Plans/canvas/01-vision.md` — the persona who runs vendor reviews
  alone (v1-binary criterion).
- CLAUDE.md persona — "vendor reviews" in the program the product
  replaces.
- `web/e2e/README.md` — spec-precondition rules.

## Dependencies

- **#394** (`fulfillFromGolden` recorder pattern) — `merged`. The mocked-
  BFF utility this spec reuses.
- **#201** (Playwright JWT migration) — `merged`. The auth fixture.
- **#139** (audit-periods + vendors export) — `merged`. The existing
  vendor export spec this slice complements.

## Anti-criteria (P0 — block merge)

- **P0-424-1.** Does NOT re-test the export email-masking already covered
  by `web/e2e/audit-periods-vendors-export.spec.ts`.
- **P0-424-2.** Does NOT drive a status transition against unseeded live
  state — use `fulfillFromGolden` where a golden exists, or defer + spill.
- **P0-424-3.** Does NOT add a cross-tenant negative case to the e2e —
  RLS denial is asserted at the Go integration tier.
- **P0-424-4.** Does NOT relax a spec precondition the docker-compose
  bring-up cannot provide (spillover instead).
- **P0-424-5.** Does NOT modify `_STATUS.md` from inside this slice's own
  commits.

## Skill mix (3-5)

- `engineering-advanced-skills:browser-automation` (Playwright spec)
- `tdd` (assert-first e2e)
- `verify` (run the app, confirm the workflow)
- `simplify` (pre-PR)

## Notes for the implementing agent

- Read `web/e2e/test-utils/fulfill-from-golden.ts` + a spec that already
  uses it (e.g. `web/e2e/dashboard.spec.ts`,
  `web/e2e/first-time-login.spec.ts`) for the recorder shape.
- Locate the vendor-review routes under `web/app/` and the backing
  handlers under `internal/api/vendors` / `internal/vendor` to confirm
  the exact status-transition + questionnaire-link surface the spec
  drives.
- Prefer a mocked-BFF spec for the transition (deterministic) and a
  light real-data assertion for the list render (proves the page wires
  to live data) — mirror the hybrid pattern other e2e specs use.
