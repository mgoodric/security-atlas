// Slice 240 — unit coverage for the policies acknowledgment-window
// disclosure constants.
//
// Pure-data tests for the constants exported by `./ack-window`. The
// DOM-level contract (the /policies list footer renders a span
// carrying the load-bearing "365-day acknowledgment window"
// substring next to the slice 246 `<ListPagination>` summary) is
// covered at the page level by the page render itself; the eventual
// Playwright spec asserts on the visible substring.
//
// Test environment is node-env, no JSX (per `web/vitest.config.ts` —
// JSX rendering is excluded by design at this surface; the page-local
// `.test.ts` files exercise pure helpers / constants only — same
// pattern as `./header-cta-future.test.ts` and `./scaffold-future.test.ts`).

import { describe, expect, it } from "vitest";

import {
  POLICY_ACK_WINDOW_CAPTION,
  POLICY_ACK_WINDOW_DAYS,
  POLICY_ACK_WINDOW_TESTID,
} from "./ack-window";

describe("policy acknowledgment-window disclosure constants (slice 240)", () => {
  it("exposes the canonical 365-day window length (AC-5)", () => {
    // P0-240-2 forbids hard-coding the literal 365 in JSX. Pinning
    // it here as the single source of truth ensures the JSX
    // composes the value out of this constant, and a future policy
    // change is one edit away.
    expect(POLICY_ACK_WINDOW_DAYS).toBe(365);
  });

  it("caption is derived from the day count (no drift between constant and text)", () => {
    // The composed caption must contain the constant value as a
    // numeric substring — proves the caption is built off the
    // constant rather than a hard-coded literal.
    expect(POLICY_ACK_WINDOW_CAPTION).toContain(String(POLICY_ACK_WINDOW_DAYS));
  });

  it("caption is non-empty and starts with the day count", () => {
    // No leading bullet or space — the page composes the separator
    // (the iteration-1 mockup uses ` · `). Pinning the leading
    // shape here means the page-side composition is the only place
    // the separator can drift.
    expect(POLICY_ACK_WINDOW_CAPTION.length).toBeGreaterThan(0);
    expect(POLICY_ACK_WINDOW_CAPTION.startsWith("365")).toBe(true);
  });

  it("caption names the cadence ('acknowledgment window') so the eventual Playwright assertion has a stable substring", () => {
    // Mirrors the slice 178 / 217 / 241 / 242 honesty-discipline
    // pattern: pin the load-bearing substring here so a future
    // copy rewrite cannot silently break the Playwright contract
    // without tripping vitest first.
    expect(POLICY_ACK_WINDOW_CAPTION.toLowerCase()).toMatch(
      /acknowledgment window/,
    );
  });

  it("caption matches the mockup exactly ('365-day acknowledgment window')", () => {
    // The mockup at `Plans/mockups/policies.html` line 279 reads:
    //     Showing 1-7 of 17 · 365-day acknowledgment window
    // The substring after the bullet is the rendered caption.
    // Pinning the literal here is the truth-telling counterpart
    // to the dynamic derivation test above — both pass at 365,
    // and the literal test breaks intentionally if a future slice
    // changes the day count without coordinating the mockup copy.
    expect(POLICY_ACK_WINDOW_CAPTION).toBe("365-day acknowledgment window");
  });

  it("caption does NOT name a tracking issue number (slice 184 D3 / 217 D3 / 241 honesty discipline)", () => {
    // Same anti-pattern as the slice 241 header CTA disclosures:
    // copy that names a specific tracking issue number can itself
    // become a HONESTY-GAP once the issue moves or is renumbered.
    // The window length is the canonical disclosure — no slice or
    // issue reference belongs in the rendered text.
    expect(POLICY_ACK_WINDOW_CAPTION).not.toMatch(/#\d+/);
    expect(POLICY_ACK_WINDOW_CAPTION.toLowerCase()).not.toMatch(/slice \d+/);
  });

  it("exposes the agreed data-testid token (AC-6)", () => {
    // Pinning the literal here keeps the page render and any
    // future slice 178 honesty-harness manifest entry in lock-step.
    expect(POLICY_ACK_WINDOW_TESTID).toBe("policies-ack-window-disclosure");
  });
});
