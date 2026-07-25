# 660 — Feature-flag state does not gate exposed nav/routes (OSCAL, Board reporting)

**Cluster:** Platform / feature-flags
**Estimate:** M (1-2d)
**Type:** JUDGMENT (gate-the-routes vs correct-the-defaults/copy is a product call)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-008).

## Narrative

Admin → Features shows `oscal.export` = OFF and `board.reporting` = OFF ("Default off
pending GA"), and the Features page copy states **"Disabling a module hides its routes."**
Yet **Vendor Claims (OSCAL)** and **Board Packs** both appear in the primary nav and are
reachable. So the stated contract (flag off → route hidden) is not enforced. Re-verified
on `main` build `2a3805b`.

`internal/featureflag/seed.go` defaults `oscal.export` and `board.reporting` to disabled
(enforced by `featureflag/seed_test.go`). The gap is that the nav + route guards don't
consult those flags. This also **exposes the broken OSCAL page** (slice 659 / ATLAS-001) —
gating the route when `oscal.export` is off removes the user-facing exposure regardless of
659's outcome.

## Threat model

No new data surface. The decision must not let a flag-gated route's API remain reachable
while only the nav link is hidden (gating must apply at the route/handler guard, not just
the nav render) — otherwise "hidden" is cosmetic.

## Acceptance criteria

- [ ] **AC-1.** JUDGMENT (decisions log): choose the resolution — (a) make the flags
      actually gate nav + routes (hide when off), or (b) correct the Features-page copy +
      flag defaults to match what actually ships GA. Default lean: **(a)** so the stated
      contract holds and the unfinished OSCAL/board surfaces are not exposed pre-GA.
- [ ] **AC-2.** If (a): when a module's flag is OFF, its **nav entry is hidden** AND its
      **routes return a clean disabled state** (not reachable, not a 500/blank) — both web
      route + the backing API guard, so the gate isn't cosmetic.
- [ ] **AC-3.** The Features-page copy ("Disabling a module hides its routes") is **true**
      after the change (or is rewritten to match reality if (b) is chosen).
- [ ] **AC-4.** Playwright coverage: with `oscal.export`/`board.reporting` off, the nav
      omits Vendor Claims + Board Packs and a direct navigation lands on the disabled state.

## Anti-criteria

- Does NOT change the flag DEFAULTS' intent (both stay off pending GA) unless (b) is the
  chosen path and the decisions log justifies it.
- Does NOT hide the nav link while leaving the route/API openly reachable (cosmetic gate).

## Dependencies

- `internal/featureflag` (seed + enabled checks) — on `main`.
- The web nav/shell + route guards (`web/components/shell/*`, route groups).
- Resolves the exposure half of slice 659 (ATLAS-001).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-008** (priority medium /
severity major). Re-tested open on build `2a3805b`. Pairs with **ATLAS-001** (slice 659).
