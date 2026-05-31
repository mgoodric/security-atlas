// Slice 394 — load `/e2e/` `route.fulfill` mock bodies from the recorded
// contract goldens under `web/lib/contracts/`.
//
// WHY THIS EXISTS (slice 334 finding P-1 / ADR-0007):
//   The e2e suite hand-writes upstream response bodies in `route.fulfill`
//   calls. Those literals can silently drift from the provider's real
//   wire shape — the exact slice-210 class of bug the contract-test tier
//   (ADR-0007) exists to catch. Slices 392 + 409 recorded the real Go
//   handler bodies into `web/lib/contracts/*.golden.json` for nine
//   endpoints. This helper teaches the e2e mocks to serve those recorded
//   bodies instead of hand-written ones, so a mock for a golden-covered
//   route cannot drift from the recorded truth.
//
// SCOPE: only the nine endpoints that HAVE a golden. Routes without a
//   golden (`/v1/risks`, `/v1/controls/*`, `/v1/board`, `/v1/policies` —
//   goldens tracked as #410 / #411) stay hand-written; see README
//   "Golden-backed route mocks" + decisions log D2/D3.
//
// ESCAPE HATCH (AC-3): the goldens carry happy-path `populated` + `empty`
//   bodies only. For error/4xx/5xx states there is no recorded body —
//   keep a hand-written `route.fulfill({ status, body })` (documented
//   below). For a populated body that needs one spec-specific value (e.g.
//   a credential-bearer `display_name` the visible assertion reads), pass
//   `options.override` — the golden stays the shape-complete base and only
//   the named field(s) change. See decisions log D3.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import type { Route } from "@playwright/test";

// The nine golden-covered endpoints (slices 349/392 + 409). A typed union,
// NOT a free string: a typo cannot fall through to a non-golden path —
// `tsc` rejects an unknown endpoint, and you cannot ask the helper for a
// route that has no recorded truth.
export type GoldenEndpoint =
  | "me"
  | "version"
  | "install-state"
  | "demo-status"
  | "framework-posture"
  | "activity"
  | "upcoming"
  | "freshness"
  | "drift";

// Endpoint -> golden filename. The files live under `web/lib/contracts/`,
// recorded by the provider-side Go contract tests (regenerate per the
// `_comment` in each golden). This map is the single place that knows the
// filename for an endpoint.
const GOLDEN_FILE: Record<GoldenEndpoint, string> = {
  me: "me.golden.json",
  version: "version.golden.json",
  "install-state": "install-state.golden.json",
  "demo-status": "demo-status.golden.json",
  "framework-posture": "framework-posture.golden.json",
  activity: "activity.golden.json",
  upcoming: "upcoming.golden.json",
  freshness: "freshness.golden.json",
  drift: "drift.golden.json",
};

// `web/e2e/test-utils/` -> `web/lib/contracts/`.
const CONTRACTS_DIR = join(__dirname, "..", "..", "lib", "contracts");

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

export interface FulfillFromGoldenOptions {
  /** HTTP status to serve the golden body under. Defaults to 200. */
  status?: number;
  /**
   * Shallow per-top-level-key merge applied OVER the golden body before
   * serialization. The escape hatch (AC-3) for a populated body that
   * needs one spec-specific value. The golden remains the shape-complete
   * base; only the named keys change. Use sparingly — prefer a bare
   * golden variant when the recorded value satisfies the assertion.
   */
  override?: Record<string, unknown>;
}

/**
 * Read a golden variant body from `web/lib/contracts/`. Exported for the
 * helper's own self-test (and any spec that needs the raw body for a
 * non-`route.fulfill` purpose). Throws loudly on an unknown endpoint or
 * variant — both are test-author bugs that should fail fast, not serve an
 * empty/stale body.
 */
export function readGoldenVariant(
  endpoint: GoldenEndpoint,
  variant: string,
): Record<string, unknown> {
  const file = GOLDEN_FILE[endpoint];
  const golden = JSON.parse(
    readFileSync(join(CONTRACTS_DIR, file), "utf8"),
  ) as Golden;
  const body = golden.variants[variant];
  if (body === undefined) {
    const available = Object.keys(golden.variants).join(", ");
    throw new Error(
      `fulfillFromGolden: golden "${file}" has no variant "${variant}" ` +
        `(available: ${available})`,
    );
  }
  return body;
}

/**
 * Serve a recorded contract-golden body via Playwright `route.fulfill`.
 *
 * The caller still owns the `page.route(pattern, …)` registration, the
 * URL glob, and any method-guard (`route.request().method() !== "GET"` →
 * `route.fallback()`). This helper owns exactly one thing: turning
 * `(endpoint, variant, override)` into a `route.fulfill(...)` of the
 * recorded body.
 *
 * @example
 *   await page.route("**\/api\/install-state", (route) =>
 *     fulfillFromGolden(route, "install-state", "fresh_install_without_tenant"),
 *   );
 *
 * @example escape-hatch override (AC-3)
 *   await page.route("**\/api\/me", async (route) => {
 *     if (route.request().method() !== "GET") return route.fallback();
 *     await fulfillFromGolden(route, "me", "synthetic_admin", {
 *       override: { display_name: "API key 1f3a" },
 *     });
 *   });
 */
export async function fulfillFromGolden(
  route: Route,
  endpoint: GoldenEndpoint,
  variant: string,
  options: FulfillFromGoldenOptions = {},
): Promise<void> {
  const base = readGoldenVariant(endpoint, variant);
  const body = options.override ? { ...base, ...options.override } : base;
  await route.fulfill({
    status: options.status ?? 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}
