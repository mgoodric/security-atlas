// Slice 225 — unit coverage for the "New control" future-state disclosure.
//
// Pure-data tests for the constants exported by `./new-control-future`.
// The DOM-level contract (the controls page renders a non-button
// affordance with `title` + `aria-label` + the agreed `data-testid`
// token) is covered by the Playwright spec at
// `web/e2e/controls-list.spec.ts` (AC-4 — quarantined behind the
// slice-082 seed harness, same as the rest of that file).
//
// Test environment is node-env, no JSX (per `web/vitest.config.ts` —
// JSX rendering is excluded by design at this surface; the page-local
// `.test.ts` files exercise pure helpers only). This mirrors the
// slice 217 vitest precedent at `../audits/oscal-export-future.test.ts`.

import { describe, expect, it } from "vitest";

import {
  NEW_CONTROL_FUTURE_REASON,
  NEW_CONTROL_FUTURE_TESTID,
} from "./new-control-future";

describe("New control future-state disclosure", () => {
  it("exposes the agreed data-testid token (AC-2)", () => {
    // The slice 225 AC-2 names the exact token the slice 178 honesty
    // harness will check against. Pinning the literal here keeps the
    // page render and the harness in lock-step.
    expect(NEW_CONTROL_FUTURE_TESTID).toBe(
      "controls-new-control-disabled-reason",
    );
  });

  it("disclosure copy is non-empty and ends with a period (sentence-shaped)", () => {
    expect(NEW_CONTROL_FUTURE_REASON.length).toBeGreaterThan(0);
    expect(NEW_CONTROL_FUTURE_REASON.trim().endsWith(".")).toBe(true);
  });

  it("disclosure copy mentions the create-control flow (AC-4 — Playwright spec asserts 'create-control' in the visible text)", () => {
    // The slice doc's AC-4 (load-bearing substring) requires the
    // visible text to surface the capability name. Pinning the
    // substring here means a future copy rewrite can't silently break
    // the Playwright contract without tripping vitest first.
    expect(NEW_CONTROL_FUTURE_REASON.toLowerCase()).toMatch(/create-control/);
  });

  it("disclosure copy names a positive next step (SCF importer or atlas CLI)", () => {
    // Slice spec AC-1 mandates the copy points the operator at the
    // current paths for instantiating a control. Pin both keywords so
    // a future copy rewrite can't drop the signposting silently.
    const lc = NEW_CONTROL_FUTURE_REASON.toLowerCase();
    expect(lc).toMatch(/scf importer/);
    expect(lc).toMatch(/atlas cli/);
  });

  it("disclosure copy frames the capability as future, not as a failure", () => {
    // The honesty discipline (slice 178 / 184 / 217 precedent) is to
    // say what WILL happen, not what's broken. Reject failure-framing
    // words like "disabled", "unavailable", "not working", "error".
    const lc = NEW_CONTROL_FUTURE_REASON.toLowerCase();
    expect(lc).not.toMatch(/disabled/);
    expect(lc).not.toMatch(/unavailable/);
    expect(lc).not.toMatch(/not working/);
    expect(lc).not.toMatch(/\berror\b/);
  });

  it("disclosure copy does NOT name a placeholder slice number (honesty discipline)", () => {
    // Per slice 184 D3 + slice 217 D3 precedent: copy that names a
    // specific tracking issue number can become a HONESTY-GAP of its
    // own once the issue moves / is renumbered. The future-state
    // framing names the capability ("create-control flow") rather
    // than a numeric ID.
    expect(NEW_CONTROL_FUTURE_REASON).not.toMatch(/#\d+/);
    expect(NEW_CONTROL_FUTURE_REASON.toLowerCase()).not.toMatch(/slice \d+/);
  });

  it("disclosure copy avoids the marketing-y ban list (CLAUDE.md tone discipline)", () => {
    // CLAUDE.md "Tone discipline" section lists banned phrases.
    // Although that list is scoped to board narratives, the same
    // discipline applies to user-facing UI copy.
    const lc = NEW_CONTROL_FUTURE_REASON.toLowerCase();
    expect(lc).not.toMatch(/best-in-class/);
    expect(lc).not.toMatch(/world-class/);
    expect(lc).not.toMatch(/industry-leading/);
    expect(lc).not.toMatch(/proud to/);
    expect(lc).not.toMatch(/exceeded/);
  });
});
