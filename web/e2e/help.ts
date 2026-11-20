// #2752 — clicking PageShell's **?** (help-panel.tsx) in e2e.
//
// The SSR'd **?** is visible, named and enabled before its subtree
// hydrates, and a click in that window opens nothing. HelpButton sets
// `data-help-ready="true"` once hydrated; click only after that.

import { expect, type Locator } from "@playwright/test";

export async function clickHelp(button: Locator) {
  // TEMP #2752 red run: click without waiting, as before the fix.
  await expect(button).toBeVisible();
  await button.click();
}
