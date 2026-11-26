// Slice 241 — unit coverage for the policies header CTA future-state
// disclosure constants.
//
// Pure-data tests for the constants exported by `./header-cta-future`.
// The DOM-level contract (the /policies header renders a non-button
// span carrying title + aria-label + the agreed data-testid token,
// replacing the formerly-disabled `Acknowledgment report` `<Button>`;
// "New policy" became a link with #2116) is covered
// by the Playwright spec at `web/e2e/policies-list.spec.ts`
// (AC-4 — quarantined behind the slice-082 seed harness, same as
// the rest of that file).
//
// Test environment is node-env, no JSX (per `web/vitest.config.ts` —
// JSX rendering is excluded by design at this surface; the page-local
// `.test.ts` files exercise pure helpers / constants only. Same
// pattern as slice 217's `oscal-export-future.test.ts` and slice 242's
// `scaffold-future.test.ts`).

import { describe, expect, it } from "vitest";

import {
  POLICIES_ACK_REPORT_FUTURE_REASON,
  POLICIES_ACK_REPORT_FUTURE_TESTID,
} from "./header-cta-future";

describe("policies acknowledgment-report future-state disclosure (slice 241)", () => {
  it("exposes the agreed data-testid token (AC-4)", () => {
    // Pinning the literal here keeps the page render and the
    // slice 178 honesty-harness manifest in lock-step.
    expect(POLICIES_ACK_REPORT_FUTURE_TESTID).toBe(
      "policies-ack-report-future",
    );
  });

  it("disclosure copy is non-empty and ends with a period (sentence-shaped)", () => {
    expect(POLICIES_ACK_REPORT_FUTURE_REASON.length).toBeGreaterThan(0);
    expect(POLICIES_ACK_REPORT_FUTURE_REASON.trim().endsWith(".")).toBe(true);
  });

  it("disclosure copy names the capability ('acknowledgment report') so AC-4 Playwright spec can assert on a stable substring", () => {
    // The slice doc's AC-4 says the Playwright spec asserts the
    // disclosure visible text contains the capability phrase.
    // Pinning the substring here means a future copy rewrite can't
    // silently break the Playwright contract without tripping
    // vitest first.
    expect(POLICIES_ACK_REPORT_FUTURE_REASON.toLowerCase()).toMatch(
      /acknowledgment report/,
    );
  });

  it("disclosure copy frames the capability as future, not as a failure", () => {
    // Per the slice 178 / 184 / 217 / 242 honesty discipline: say
    // what WILL happen, not what is broken. Reject failure-framing
    // words like "disabled", "unavailable", "not working", "error".
    const lc = POLICIES_ACK_REPORT_FUTURE_REASON.toLowerCase();
    expect(lc).not.toMatch(/disabled/);
    expect(lc).not.toMatch(/unavailable/);
    expect(lc).not.toMatch(/not working/);
    expect(lc).not.toMatch(/\berror\b/);
  });

  it("disclosure copy does NOT name a placeholder slice number (slice 184 D3 / 217 D3 honesty)", () => {
    // Per slice 184 D3 + slice 217 D3 + slice 242 D3 precedent:
    // copy that names a specific tracking issue number can itself
    // become a HONESTY-GAP once the issue moves or is renumbered.
    // The future-state framing names the capability ("acknowledgment
    // report") rather than a numeric ID.
    expect(POLICIES_ACK_REPORT_FUTURE_REASON).not.toMatch(/#\d+/);
    expect(POLICIES_ACK_REPORT_FUTURE_REASON.toLowerCase()).not.toMatch(
      /slice \d+/,
    );
  });
});
