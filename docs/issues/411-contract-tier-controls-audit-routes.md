# 411 — Contract-tier rollout: controls-detail + audit-workspace routes

**Cluster:** Quality
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `merged` (`cfaa30a9`, #958 — 4 routes recorded; tail → spillover 412)

## Narrative

Surfaced during slice 409, captured per continuous-batch policy.

Slice 409 rolled the golden-file contract tier (ADR-0007) out to the five
dashboard panel routes and **deferred** the controls-detail
(`/v1/controls/*`) and audit-workspace (`/v1/audit/*`) route families
(`docs/audit-log/409-contract-tier-rollout-dashboard-decisions.md` D1).

These are large, multi-route, pool-backed surfaces (controldetail
coverage/effectiveness/history/policies/attestations; audit
periods/populations/samples/walkthroughs/notes). Each route family needs
its own bounded Option-A read seam, and there are many routes — well beyond
what a single slice should absorb. They are also outside the dashboard-
panel core that the v1-binary-relevant e2e dashboard view traverses, so
they are lower priority than the dashboard panels #409 covered.

## What ships (when unblocked)

- Per-route-family unexported read seams (Option A; P0-409-2 — internal,
  no public-API widening).
- Provider recorders + consumer asserts for the highest-traffic
  controls-detail and audit-workspace routes the e2e suite traverses
  (prioritize the ones #394's `fulfillFromGolden` migration would
  otherwise hand-mock).
- Drift-sensitivity proof per new endpoint.
- Likely split into two slices (controls; audit) if the seam work is large.

## Acceptance criteria

- [ ] **AC-1.** Read seams on the targeted controls/audit handlers;
      recorders on the unit surface (no DB; no integration tag — P0-409-1).
- [ ] **AC-2.** Goldens + consumer asserts land for the targeted routes;
      any further deferrals documented.
- [ ] **AC-3.** Drift sensitivity proven on ≥1 new endpoint.
- [ ] **AC-4.** Zero-new-gate.

## Dependencies

- **#409** — `merged`. Established the Option-A seam pattern + shared
  recorder helper.
- **Appetite** — `blocked` until a maintainer prioritizes the controls/
  audit golden coverage. The dashboard-panel core (#409) + risks (#410)
  cover the v1-binary-relevant e2e dashboard view; these route families are
  a v2 quality follow-on.

## Cross-references

- ADR-0007 (`docs/adr/0007-contract-test-tier.md`)
- Slice 409 decisions log D1 + D6 (the deferral rationale)
