// Slice 178 — forward-looking-UI heuristics (AC-5).
//
// Three heuristics run against the live DOM after `networkidle`:
//
//   AC-5a — dead anchors. Anchors whose `href` is literally `#` (no
//           destination), or whose `href` is a relative path that, when
//           resolved against the page URL, 404s on the live app.
//
//   AC-5b — "coming soon" buttons. Buttons with `disabled` AND a
//           tooltip / aria-label / inner text matching /coming soon|
//           not yet|placeholder/i. Disabled-on-load is a strong
//           anti-pattern signal.
//
//   AC-5c — unset feature flags. Elements with a `data-feature-flag`
//           attribute referencing a flag NOT in the current flag set.
//           The current flag set is derived from `window.__ATLAS_FLAGS`
//           if the app exposes it, otherwise treated as empty and the
//           heuristic surfaces every `data-feature-flag` element it
//           sees (a noisy but honest signal — the maintainer adds the
//           flag to `allowedExtraTestIds` or removes the element).

import type { Page } from "@playwright/test";

import type { LiveFingerprint } from "./mockup-diff";

export type DeadAnchor = { href: string; text: string };
export type ComingSoonButton = { text: string; ariaLabel: string | null };
export type UnsetFeatureFlag = { flag: string; selector: string };

const COMING_SOON_PATTERN = /(coming soon|not yet|placeholder)/i;

/**
 * Enumerate every `data-testid` on the page. Returned sorted +
 * deduplicated for stable diffs.
 *
 * Excludes testids on `<template>` children (Next.js sometimes ships
 * hidden template DOM).
 */
export async function captureTestIds(page: Page): Promise<string[]> {
  const ids = await page.evaluate(() => {
    const out: string[] = [];
    const nodes = document.querySelectorAll("[data-testid]");
    for (const n of Array.from(nodes)) {
      const tid = (n as HTMLElement).dataset.testid;
      if (!tid) continue;
      // Skip if any ancestor is a <template> (hidden DOM).
      let cur: HTMLElement | null = n as HTMLElement;
      let inTemplate = false;
      while (cur) {
        if (cur.tagName === "TEMPLATE") {
          inTemplate = true;
          break;
        }
        cur = cur.parentElement;
      }
      if (!inTemplate) out.push(tid);
    }
    return out;
  });
  return Array.from(new Set(ids)).sort();
}

/**
 * AC-5a — dead anchors. Returns the list of anchors whose href is
 * either `#`, the empty string, or (for relative hrefs) returns 404
 * when fetched through the page's HTTP context.
 *
 * Note: external (`http(s)://...`) hrefs are NOT probed — they may be
 * legitimate cross-origin links. The audit cares about INTERNAL
 * placeholders.
 */
export async function captureDeadAnchors(page: Page): Promise<DeadAnchor[]> {
  const anchors = await page.evaluate(() => {
    const out: Array<{ href: string; text: string; resolvedURL: string }> = [];
    const links = document.querySelectorAll("a[href]");
    for (const a of Array.from(links)) {
      const href = (a as HTMLAnchorElement).getAttribute("href") ?? "";
      const text = (a as HTMLElement).textContent?.trim() ?? "";
      const resolvedURL = (a as HTMLAnchorElement).href;
      out.push({ href, text, resolvedURL });
    }
    return out;
  });

  const dead: DeadAnchor[] = [];
  for (const a of anchors) {
    // Literal `#` or empty = dead by construction.
    if (a.href === "#" || a.href.trim() === "") {
      dead.push({ href: a.href, text: a.text });
      continue;
    }
    // Skip external + special schemes (mailto, tel, javascript).
    if (
      a.href.startsWith("http://") ||
      a.href.startsWith("https://") ||
      a.href.startsWith("mailto:") ||
      a.href.startsWith("tel:") ||
      a.href.startsWith("javascript:")
    ) {
      // Allow internal-host http(s) only via the resolvedURL check
      // below; bare http://external.com is left alone.
      const pageHost = new URL(page.url()).host;
      try {
        const linkHost = new URL(a.resolvedURL).host;
        if (linkHost !== pageHost) continue;
      } catch {
        continue;
      }
    }
    // Internal anchor (in-page #fragment) is not "dead" — it scrolls.
    if (a.href.startsWith("#") && a.href.length > 1) continue;
    // Resolve relative to the page and probe.
    try {
      const probe = await page.request.head(a.resolvedURL);
      if (probe.status() === 404) {
        dead.push({ href: a.href, text: a.text });
      }
    } catch {
      // Network error — treat as a non-finding to avoid false flags
      // from transient infrastructure issues.
    }
  }
  return dead;
}

/**
 * AC-5b — "coming soon" placeholder buttons. A button is flagged if
 * (a) it has the `disabled` attribute AND (b) its aria-label OR inner
 * text contains the coming-soon pattern. The `disabled` requirement
 * keeps us from flagging legitimate primary-action buttons whose label
 * contains "coming up" / similar.
 */
export async function captureComingSoonButtons(
  page: Page,
): Promise<ComingSoonButton[]> {
  return page.evaluate((patternSource: string) => {
    const out: Array<{ text: string; ariaLabel: string | null }> = [];
    const pat = new RegExp(patternSource, "i");
    const buttons = document.querySelectorAll(
      'button[disabled], button[aria-disabled="true"]',
    );
    for (const b of Array.from(buttons)) {
      const text = (b as HTMLElement).textContent?.trim() ?? "";
      const ariaLabel = (b as HTMLElement).getAttribute("aria-label");
      const title = (b as HTMLElement).getAttribute("title");
      const haystack = [text, ariaLabel ?? "", title ?? ""].join(" ");
      if (pat.test(haystack)) {
        out.push({ text, ariaLabel });
      }
    }
    return out;
  }, COMING_SOON_PATTERN.source);
}

/**
 * AC-5c — unset feature flags. Elements carrying `data-feature-flag`
 * whose flag name is NOT present in `window.__ATLAS_FLAGS` (if the app
 * exposes that). If the app does not expose a flag-set object, EVERY
 * `data-feature-flag` element is surfaced — the maintainer adds an
 * allow-list entry or removes the element.
 */
export async function captureUnsetFeatureFlags(
  page: Page,
): Promise<UnsetFeatureFlag[]> {
  return page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const flagsRaw = (window as any).__ATLAS_FLAGS;
    const known = new Set<string>();
    if (flagsRaw && typeof flagsRaw === "object") {
      for (const [k, v] of Object.entries(flagsRaw)) {
        if (v === true) known.add(k);
      }
    }
    const out: Array<{ flag: string; selector: string }> = [];
    const nodes = document.querySelectorAll("[data-feature-flag]");
    for (const n of Array.from(nodes)) {
      const flag = (n as HTMLElement).getAttribute("data-feature-flag") ?? "";
      if (!flag) continue;
      if (known.has(flag)) continue;
      const tid = (n as HTMLElement).getAttribute("data-testid");
      const selector = tid
        ? `[data-testid="${tid}"]`
        : `${(
            n as Element
          ).tagName.toLowerCase()}[data-feature-flag="${flag}"]`;
      out.push({ flag, selector });
    }
    return out;
  });
}

/**
 * Build the full fingerprint for one route. Composes the four
 * captures above.
 */
export async function captureFingerprint(
  page: Page,
  route: string,
): Promise<LiveFingerprint> {
  const testIds = await captureTestIds(page);
  const deadAnchors = await captureDeadAnchors(page);
  const comingSoonButtons = await captureComingSoonButtons(page);
  const unsetFeatureFlags = await captureUnsetFeatureFlags(page);
  return {
    route,
    testIds,
    deadAnchors,
    comingSoonButtons,
    unsetFeatureFlags,
  };
}
