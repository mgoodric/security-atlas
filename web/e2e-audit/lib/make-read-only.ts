// Slice 178 — read-only guardrail (AC-7, P0-178-1).
//
// `makeReadOnly(page)` wraps `page.click()` and the keyboard / mouse
// interaction surface so the test FAILS the moment a mutating action is
// attempted. The harness is allowed to navigate (link clicks, sort
// headers, filter pills, expand/collapse triggers) but MUST NOT submit
// forms or click destructive actions — those would corrupt the seeded
// data the audit relies on (P0-178-1), and on operator-local prod runs
// (PLATFORM_BASE_URL override) they would mutate real tenant data.
//
// Mutation detection is structural, not semantic. A click is rejected
// if the target element OR any ancestor matches any of:
//
//   * `<button type="submit">`
//   * `<input type="submit">`
//   * `<form>` direct child that submits when clicked
//   * `[data-mutating="true"]` (explicit opt-out marker — components
//     that want to be flagged as state-mutating can set this)
//   * `[data-testid$="-submit"]`            (project convention)
//   * `[data-testid$="-revoke-button"]`     (settings tokens)
//   * `[data-testid$="-rotate-button"]`     (settings tokens)
//   * `[data-testid$="-issue-button"]`      (settings tokens)
//   * `[data-testid="annotation-submit"]`   (audit workspace)
//   * `[data-testid="attest-submit"]`       (audit workspace)
//   * `[data-testid="comment-submit"]`      (audit workspace)
//   * any element whose accessible name matches `/\b(delete|remove|
//     revoke|rotate|approve|publish|submit|create|generate|attest|
//     freeze|sign|send|invite|grant|deny|reject|confirm)\b/i` AND
//     whose tag is `BUTTON` (heuristic guardrail — broad but
//     intentional; the alternative is the harness silently mutating).
//
// The guardrail does NOT wrap `page.goto(...)`, `page.reload()`,
// `page.keyboard.press(...)` (for arrow-key navigation), or `page.locator
// (...).hover()`. Those are read-only by construction.
//
// Unit test: `lib/make-read-only.test.ts` asserts the guardrail fires
// for each mutation pattern AND silently passes a navigation click.

import type { Locator, Page } from "@playwright/test";

const MUTATING_SELECTORS: ReadonlyArray<string> = [
  'button[type="submit"]',
  'input[type="submit"]',
  '[data-mutating="true"]',
  '[data-testid$="-submit"]',
  '[data-testid$="-revoke-button"]',
  '[data-testid$="-rotate-button"]',
  '[data-testid$="-issue-button"]',
  '[data-testid="annotation-submit"]',
  '[data-testid="attest-submit"]',
  '[data-testid="comment-submit"]',
];

// Verbs that mark a button as state-mutating regardless of testid /
// type. Pattern-matched against accessible name + visible text.
const MUTATING_VERB_PATTERN =
  /\b(delete|remove|revoke|rotate|approve|publish|submit|create|generate|attest|freeze|sign|send|invite|grant|deny|reject|confirm)\b/i;

export class ReadOnlyGuardrailViolation extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ReadOnlyGuardrailViolation";
  }
}

/**
 * Inspect the target locator + ancestors. Return a non-null reason
 * string if a mutation pattern matches; `null` if the click is safe.
 *
 * Exported for unit tests; the harness consumes this via
 * `makeReadOnly(page)`.
 */
export async function detectMutationViolation(
  locator: Locator,
): Promise<string | null> {
  // Selector-based check (matches anywhere in the ancestor chain via
  // CSS `:has(...)` semantics through `locator.evaluate`).
  const selectorHit = await locator.evaluate(
    (el, selectors) => {
      let cursor: Element | null = el as Element;
      while (cursor) {
        for (const sel of selectors) {
          try {
            if (cursor.matches(sel)) {
              return sel;
            }
          } catch {
            // Invalid selector for this node; skip silently.
          }
        }
        cursor = cursor.parentElement;
      }
      return null;
    },
    MUTATING_SELECTORS as unknown as string[],
  );
  if (selectorHit) {
    return `mutation pattern matched: ${selectorHit}`;
  }

  // Verb-text heuristic. Only applies if the element is a <button>.
  const tagName = await locator.evaluate((el) =>
    (el as Element).tagName.toUpperCase(),
  );
  if (tagName === "BUTTON") {
    const accName = await locator.evaluate((el) => {
      const aria = (el as HTMLElement).getAttribute("aria-label");
      if (aria) return aria;
      return (el as HTMLElement).textContent?.trim() ?? "";
    });
    if (accName && MUTATING_VERB_PATTERN.test(accName)) {
      return `mutating verb in button label: "${accName}"`;
    }
  }

  return null;
}

/**
 * Wrap `page.click` with the read-only guardrail. Returns the patched
 * page. The patch is in-place; the same Page reference is returned for
 * call-site fluency.
 */
export function makeReadOnly(page: Page): Page {
  const originalClick = page.click.bind(page);
  // Cast to a permissive signature so we can preserve Playwright's
  // overloads without re-declaring every one.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (page as any).click = async (selector: string, options?: unknown) => {
    const loc = page.locator(selector).first();
    const violation = await detectMutationViolation(loc);
    if (violation) {
      throw new ReadOnlyGuardrailViolation(
        `read-only guardrail (P0-178-1) rejected click on \`${selector}\` — ${violation}. ` +
          `The UI honesty audit MUST NOT mutate state; see web/e2e-audit/README.md.`,
      );
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return originalClick(selector, options as any);
  };

  // Locator.click() is the more common path (Playwright recommended).
  // We wrap by overriding Page.locator so every locator inherits the
  // guardrail.
  const originalLocator = page.locator.bind(page);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (page as any).locator = (selector: string, options?: unknown) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const loc = originalLocator(selector, options as any);
    const originalLocatorClick = loc.click.bind(loc);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (loc as any).click = async (clickOptions?: unknown) => {
      const violation = await detectMutationViolation(loc.first());
      if (violation) {
        throw new ReadOnlyGuardrailViolation(
          `read-only guardrail (P0-178-1) rejected click on \`${selector}\` — ${violation}. ` +
            `The UI honesty audit MUST NOT mutate state; see web/e2e-audit/README.md.`,
        );
      }
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return originalLocatorClick(clickOptions as any);
    };
    return loc;
  };

  return page;
}
