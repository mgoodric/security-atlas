// Slice 233 — unit tests for the /evidence page's "Push evidence" CTA
// pure-logic module.
//
// web/vitest.config.ts pins env=node + no JSX runtime, so we cannot
// render `page.tsx` directly. Instead we pin the constants the JSX
// reads (slice 219 / 222 / 248 precedent). The Playwright spec at
// web/e2e/evidence-list.spec.ts carries the live render assertion as
// a quarantined contract (un-commented when the slice 082 seed
// harness lands).
//
// What this suite guards:
//   AC-1 — the CTA is a real, non-disabled affordance (its href is a
//   non-empty string starting with `/docs/`, so the link points at
//   the canonical CLI quickstart and is navigable — the previous
//   permanently-disabled `<Button>` is gone).
//   AC-2 — the subtitle suffix concatenates the prefix + label that
//   the JSX renders into the `<p>` second sentence.

import { describe, expect, test } from "vitest";

import {
  PUSH_CTA_HREF,
  PUSH_CTA_LABEL,
  PUSH_CTA_SUBTITLE_PREFIX,
  pushCtaSubtitleSuffix,
} from "./push-cta";

describe("slice 233 — /evidence Push CTA constants", () => {
  test("PUSH_CTA_LABEL is the slice 233 spec label (with right-arrow)", () => {
    // The spec (docs/issues/233...md, AC-1) pins the label as
    // "Push evidence →" — the trailing Unicode right-arrow is the
    // signpost that the surface navigates somewhere, not a dead
    // button.
    expect(PUSH_CTA_LABEL).toBe("Push evidence →");
  });

  test("PUSH_CTA_HREF points at the canonical CLI push doc", () => {
    // Destination decision D1 in docs/audit-log/233-decisions.md:
    // the /docs/primitives/evidence anchor section "Pushing evidence
    // from your own tools" carries the canonical
    // `just atlas-cli evidence push` example. The leading `/docs/`
    // segment is required (mkdocs-material docs site is mounted
    // under that prefix in the atlas-edge deployment).
    expect(PUSH_CTA_HREF).toBe(
      "/docs/primitives/evidence#pushing-evidence-from-your-own-tools",
    );
    expect(PUSH_CTA_HREF.startsWith("/docs/")).toBe(true);
  });

  test("PUSH_CTA_HREF is a non-empty, navigable path (not '#' / not empty)", () => {
    // Negative test: the prior shape was a `disabled` button with no
    // href at all. The link MUST resolve to a real route — `#` or
    // empty string is the disabled-affordance smell we are closing.
    expect(PUSH_CTA_HREF).not.toBe("");
    expect(PUSH_CTA_HREF).not.toBe("#");
    expect(PUSH_CTA_HREF.length).toBeGreaterThan(1);
  });

  test("PUSH_CTA_SUBTITLE_PREFIX is the literal text the `<p>` renders before the link", () => {
    // The JSX in page.tsx renders:
    //   <p>...possible. {PUSH_CTA_SUBTITLE_PREFIX}<a>{PUSH_CTA_LABEL}</a></p>
    // The prefix MUST end with a space so the `<a>` does not bump
    // up against the preceding "see" word.
    expect(PUSH_CTA_SUBTITLE_PREFIX).toBe("Push via CLI or SDK — see ");
    expect(PUSH_CTA_SUBTITLE_PREFIX.endsWith(" ")).toBe(true);
  });

  test("pushCtaSubtitleSuffix concatenates prefix + label as a single string", () => {
    // The composed string is what a screen reader announces when the
    // `<p>` is read aloud — pin the concatenation so a future refactor
    // does not introduce double-space, lost-space, or reordering bugs.
    expect(pushCtaSubtitleSuffix()).toBe(
      "Push via CLI or SDK — see Push evidence →",
    );
  });
});
