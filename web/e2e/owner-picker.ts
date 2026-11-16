// #2129 — pick an owner with the entity picker (components/ui/entity-picker).
//
// Risk and vendor owners are users, chosen from the tenant's directory
// (`/api/people`). Specs that only need *an* owner pick the first user
// listed: the signed-in test user is always one. Returns the picked name,
// the text the picker's input then shows.

import type { Page } from "@playwright/test";

import { expect } from "./fixtures";

export async function pickFirstUser(
  page: Page,
  testId: string,
): Promise<string> {
  const input = page.getByTestId(testId);
  await input.click();
  const option = page.getByTestId(`${testId}-option`).first();
  await expect(option).toBeVisible({ timeout: 30_000 });
  await option.click();
  const picked = await input.inputValue();
  expect(picked).not.toBe("");
  return picked;
}
