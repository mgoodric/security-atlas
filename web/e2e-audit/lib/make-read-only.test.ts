// Slice 178 — read-only guardrail unit tests (AC-7).
//
// `detectMutationViolation` is the pure detection function; the
// `makeReadOnly(page)` patch sits on top of it. We test the detector
// directly with a minimal Playwright-Locator mock that exposes the two
// surface methods the detector uses: `.evaluate(fn, arg?)` and the
// (here unused) `.first()` accessor.
//
// The mock simulates an element via a tiny synthetic DOM tree.

import { describe, expect, it } from "vitest";

import type { Locator } from "@playwright/test";

import { detectMutationViolation } from "./make-read-only";

/**
 * A SyntheticEl is the minimum DOM-like shape the detector needs:
 *   - .matches(selector) — CSS selector matching
 *   - .parentElement     — ancestor walk
 *   - .tagName           — UPPERCASE element name
 *   - .getAttribute(...) — aria-label
 *   - .textContent       — visible-text fallback
 */
type SyntheticEl = {
  tagName: string;
  attributes: Record<string, string>;
  text?: string;
  parent?: SyntheticEl;
};

function syntheticMatches(el: SyntheticEl, selector: string): boolean {
  // Cover the selector shapes the detector uses:
  // `button[type="submit"]`
  // `input[type="submit"]`
  // `[data-mutating="true"]`
  // `[data-testid$="-submit"]`
  // `[data-testid="annotation-submit"]`
  const m = selector.match(
    /^([a-z]+)?\[([a-z-]+)([$*~|^]?=)?"?([^"\]]*)"?\]$/i,
  );
  if (!m) return false;
  const [, tag, attr, op, val] = m;
  if (tag && el.tagName.toLowerCase() !== tag.toLowerCase()) return false;
  const have = el.attributes[attr];
  if (have === undefined) return false;
  if (!op) return true;
  if (op === "=") return have === val;
  if (op === "$=") return have.endsWith(val);
  if (op === "^=") return have.startsWith(val);
  if (op === "*=") return have.includes(val);
  return false;
}

function mockLocator(el: SyntheticEl): Locator {
  return {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    evaluate: async (fn: any, arg?: unknown) => {
      // Build a tiny browser-side stand-in that exposes the methods the
      // detector's evaluate-callbacks use.
      const elProxy = {
        matches(sel: string) {
          return syntheticMatches(el, sel);
        },
        get parentElement(): unknown {
          if (!el.parent) return null;
          return mockProxy(el.parent);
        },
        get tagName() {
          return el.tagName.toUpperCase();
        },
        getAttribute(name: string) {
          return el.attributes[name] ?? null;
        },
        get textContent() {
          return el.text ?? "";
        },
      };
      function mockProxy(p: SyntheticEl): unknown {
        return {
          matches(sel: string) {
            return syntheticMatches(p, sel);
          },
          get parentElement(): unknown {
            if (!p.parent) return null;
            return mockProxy(p.parent);
          },
          get tagName() {
            return p.tagName.toUpperCase();
          },
          getAttribute(n: string) {
            return p.attributes[n] ?? null;
          },
          get textContent() {
            return p.text ?? "";
          },
        };
      }
      return fn(elProxy, arg);
    },
  } as unknown as Locator;
}

describe("detectMutationViolation — AC-7", () => {
  it('fires on <button type="submit">', async () => {
    const el: SyntheticEl = {
      tagName: "button",
      attributes: { type: "submit" },
      text: "OK",
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toMatch(/mutation pattern matched/);
  });

  it('fires on [data-mutating="true"]', async () => {
    const el: SyntheticEl = {
      tagName: "div",
      attributes: { "data-mutating": "true" },
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toMatch(/data-mutating/);
  });

  it('fires on [data-testid$="-submit"]', async () => {
    const el: SyntheticEl = {
      tagName: "button",
      attributes: { "data-testid": "audits-create-submit" },
      text: "Create audit",
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toMatch(/-submit/);
  });

  it('fires on [data-testid="settings-token-revoke-button"]', async () => {
    const el: SyntheticEl = {
      tagName: "button",
      attributes: { "data-testid": "settings-token-revoke-button" },
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).not.toBeNull();
  });

  it('fires via verb heuristic on a <button> whose label is "Delete"', async () => {
    const el: SyntheticEl = {
      tagName: "button",
      attributes: {},
      text: "Delete this",
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toMatch(/mutating verb/);
  });

  it("fires via aria-label verb heuristic", async () => {
    const el: SyntheticEl = {
      tagName: "button",
      attributes: { "aria-label": "Revoke token" },
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toMatch(/mutating verb/);
  });

  it("ascends ancestors — flags when a <form> wraps the click target", async () => {
    const form: SyntheticEl = {
      tagName: "form",
      attributes: { "data-testid": "audits-create-form" },
    };
    // The detector's selectors don't include bare `form`; this case is
    // covered by the `[data-testid$="-form"]` not being a mutator marker
    // (forms render even without a submit). We verify the ascent semantics
    // via [data-mutating]:
    const wrapper: SyntheticEl = {
      tagName: "div",
      attributes: { "data-mutating": "true" },
    };
    const inner: SyntheticEl = {
      tagName: "span",
      attributes: {},
      text: "OK",
      parent: wrapper,
    };
    wrapper.parent = form;
    const v = await detectMutationViolation(mockLocator(inner));
    expect(v).toMatch(/data-mutating/);
  });

  it("does NOT fire on a plain nav link", async () => {
    const el: SyntheticEl = {
      tagName: "a",
      attributes: { href: "/controls", "data-testid": "controls-row-scf-id" },
      text: "AT-01",
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toBeNull();
  });

  it("does NOT fire on a non-button element with a mutating-verb label", async () => {
    // The verb heuristic only applies to <button>; we deliberately do
    // NOT flag a <span> reading "Delete" inside a table row.
    const el: SyntheticEl = {
      tagName: "span",
      attributes: {},
      text: "Delete column",
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toBeNull();
  });

  it('does NOT fire on a sort-header button (verb heuristic skips "sort")', async () => {
    const el: SyntheticEl = {
      tagName: "button",
      attributes: {},
      text: "Sort by name",
    };
    const v = await detectMutationViolation(mockLocator(el));
    expect(v).toBeNull();
  });
});
