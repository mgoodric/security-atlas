// Slice 242 — unit coverage for the policies scaffold future-state
// disclosure constants.
//
// Pure-data tests for the constants exported by `./scaffold-future`.
// The DOM-level contract (the /policies empty-state renders the
// disclosure body + the agreed `data-testid` token + no longer
// renders the lying CTA button) is covered by the Playwright spec
// at `web/e2e/policies-list.spec.ts` (AC-7 — quarantined behind
// the slice-082 seed harness, same as the rest of that file).
//
// Test environment is node-env, no JSX (per `web/vitest.config.ts` —
// JSX rendering is excluded by design at this surface; the
// page-local `.test.ts` files exercise pure helpers / constants only.
// Same pattern as slice 217's `oscal-export-future.test.ts`).

import { describe, expect, it } from "vitest";

import {
  POLICIES_SCAFFOLD_FUTURE_BODY,
  POLICIES_SCAFFOLD_FUTURE_TESTID,
} from "./scaffold-future";

describe("policies scaffold future-state disclosure (slice 242)", () => {
  it("exposes the agreed data-testid token (AC-7)", () => {
    // Pinning the literal here keeps the page render and the
    // slice 178 honesty-harness manifest in lock-step.
    expect(POLICIES_SCAFFOLD_FUTURE_TESTID).toBe("policies-scaffold-future");
  });

  it("disclosure body is non-empty and ends with a period (sentence-shaped)", () => {
    expect(POLICIES_SCAFFOLD_FUTURE_BODY.length).toBeGreaterThan(0);
    expect(POLICIES_SCAFFOLD_FUTURE_BODY.trim().endsWith(".")).toBe(true);
  });

  it("disclosure copy names the capability ('policy scaffold') so AC-7 Playwright spec can assert on a stable substring", () => {
    // The slice doc's AC-7 says the Playwright spec asserts the
    // empty-state body contains the capability phrase. Pinning the
    // substring here means a future copy rewrite can't silently
    // break the Playwright contract without tripping vitest first.
    expect(POLICIES_SCAFFOLD_FUTURE_BODY.toLowerCase()).toMatch(
      /policy scaffold/,
    );
  });

  it("disclosure copy frames the capability as future, not as a failure", () => {
    // Per the slice 178 / 184 / 217 honesty discipline: say what
    // WILL happen, not what is broken. Reject failure-framing
    // words like "disabled", "unavailable", "not working", "error".
    const lc = POLICIES_SCAFFOLD_FUTURE_BODY.toLowerCase();
    expect(lc).not.toMatch(/disabled/);
    expect(lc).not.toMatch(/unavailable/);
    expect(lc).not.toMatch(/not working/);
    expect(lc).not.toMatch(/\berror\b/);
  });

  it("disclosure copy does NOT name a placeholder slice number (slice 184 D3 / 217 D3 honesty)", () => {
    // Per slice 184 D3 + slice 217 D3 precedent: copy that names
    // a specific tracking issue number can itself become a
    // HONESTY-GAP once the issue moves or is renumbered. The
    // future-state framing names the capability ("policy scaffold
    // wizard") rather than a numeric ID.
    expect(POLICIES_SCAFFOLD_FUTURE_BODY).not.toMatch(/#\d+/);
    expect(POLICIES_SCAFFOLD_FUTURE_BODY.toLowerCase()).not.toMatch(
      /slice \d+/,
    );
  });

  it("disclosure copy names the platform API endpoint so the empty-state is a signpost, not a dead end", () => {
    // The body text gives the operator a concrete next action —
    // creating drafts via `POST /v1/policies` — so the empty-state
    // converts dead chrome into useful signposting. Slice 217 D3
    // precedent: "Action hint baked in. 'Open a period to export'
    // tells the operator HOW to reach the working export."
    expect(POLICIES_SCAFFOLD_FUTURE_BODY).toMatch(/POST \/v1\/policies/);
  });
});
