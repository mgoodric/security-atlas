// Slice 217 — unit coverage for the OSCAL export future-state disclosure.
//
// Pure-data tests for the constants exported by `./oscal-export-future`.
// The DOM-level contract (the audits page renders a non-button affordance
// with `title` + `aria-label` + the agreed `data-testid` token) is
// covered by the Playwright spec at `web/e2e/audits-list.spec.ts`
// (AC-A4 — quarantined behind the slice-082 seed harness, same as the
// rest of that file).
//
// Test environment is node-env, no JSX (per `web/vitest.config.ts` —
// JSX rendering is excluded by design at this surface; the page-local
// `.test.ts` files exercise pure helpers only).

import { describe, expect, it } from "vitest";

import {
  OSCAL_EXPORT_FUTURE_REASON,
  OSCAL_EXPORT_FUTURE_TESTID,
} from "./oscal-export-future";

describe("OSCAL export future-state disclosure", () => {
  it("exposes the agreed data-testid token (AC-A2)", () => {
    // The slice 217 AC-A2 names the exact token the slice 178 honesty
    // harness will check against. Pinning the literal here keeps the
    // page render and the harness in lock-step.
    expect(OSCAL_EXPORT_FUTURE_TESTID).toBe("audits-oscal-export-future");
  });

  it("disclosure copy is non-empty and ends with a period (sentence-shaped)", () => {
    expect(OSCAL_EXPORT_FUTURE_REASON.length).toBeGreaterThan(0);
    expect(OSCAL_EXPORT_FUTURE_REASON.trim().endsWith(".")).toBe(true);
  });

  it("disclosure copy mentions the per-period home (AC-A4 — Playwright spec asserts 'per-period' in the visible text)", () => {
    // The slice doc's AC-A4 says the Playwright spec asserts the
    // disclosure visible text contains "per-period". Pinning the
    // substring here means a future copy rewrite can't silently break
    // the Playwright contract without tripping vitest first.
    expect(OSCAL_EXPORT_FUTURE_REASON.toLowerCase()).toMatch(/per-period/);
  });

  it("disclosure copy frames the capability as future, not as a failure", () => {
    // The honesty discipline (slice 178 / 184 precedent) is to say what
    // WILL happen, not what's broken. Reject failure-framing words like
    // "disabled", "unavailable", "not working", "error".
    const lc = OSCAL_EXPORT_FUTURE_REASON.toLowerCase();
    expect(lc).not.toMatch(/disabled/);
    expect(lc).not.toMatch(/unavailable/);
    expect(lc).not.toMatch(/not working/);
    expect(lc).not.toMatch(/\berror\b/);
  });

  it("disclosure copy does NOT name a placeholder slice number (P0-217-3 honesty)", () => {
    // Per slice 184 D3 precedent: copy that names a specific tracking
    // issue number can become a HONESTY-GAP of its own once the issue
    // moves / is renumbered. The future-state framing names the
    // capability ("per-period detail view") rather than a numeric ID.
    expect(OSCAL_EXPORT_FUTURE_REASON).not.toMatch(/#\d+/);
    expect(OSCAL_EXPORT_FUTURE_REASON.toLowerCase()).not.toMatch(/slice \d+/);
  });
});
