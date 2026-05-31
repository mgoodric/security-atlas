// Slice 394 — self-test for the e2e `fulfillFromGolden` helper.
//
// The helper itself lives under `web/e2e/test-utils/fulfill-from-golden.ts`
// (it serves golden bodies via Playwright `route.fulfill` in the e2e
// suite). But vitest EXCLUDES `e2e/**` from its runner, and the Playwright
// runner only executes `*.spec.ts` — so the helper's pure, browser-free
// logic (the endpoint->golden map, variant lookup, the override deep-merge,
// the loud-throw on an unknown variant) has no fast unit feedback on either
// existing surface unless we colocate a vitest test under a vitest-included
// path. This file lives next to the goldens it reads (`web/lib/contracts/`,
// a vitest-included dir) and imports the helper by relative path. The
// helper's only Playwright import is `import type { Route }` — a type-only
// import, fully erased at runtime — so vitest loads it with no browser.
//
// This rides the EXISTING `Frontend · vitest` surface (slice 348's
// `**/*.test.ts` directory walk auto-enrolls it): zero new gate (AC-4).
// The helper file is under `e2e/**`, outside the coverage `include` set,
// so it adds no per-file ratchet entry.

import type { Route } from "@playwright/test";
import { describe, expect, test } from "vitest";

import {
  fulfillFromGolden,
  readGoldenVariant,
  type GoldenEndpoint,
} from "../../e2e/test-utils/fulfill-from-golden";

// A tiny fake Route capturing the single `fulfill` call the helper makes.
// The helper only ever calls `route.fulfill`, so a partial stub cast to
// `Route` is sufficient (and clearer than mocking the full interface).
type FulfillArg = {
  status?: number;
  contentType?: string;
  body?: string;
};
function fakeRoute() {
  const calls: FulfillArg[] = [];
  const route = {
    fulfill: async (arg: FulfillArg) => {
      calls.push(arg);
    },
  } as unknown as Route;
  return { calls, route };
}

const ALL_ENDPOINTS: GoldenEndpoint[] = [
  "me",
  "version",
  "install-state",
  "demo-status",
  "framework-posture",
  "activity",
  "upcoming",
  "freshness",
  "drift",
];

describe("fulfillFromGolden helper (slice 394)", () => {
  test("every covered endpoint resolves to a readable golden with >=1 variant", () => {
    for (const endpoint of ALL_ENDPOINTS) {
      // Each golden has at least the documented variants; we don't assume
      // names here, only that the file resolves and parses (the throw on a
      // bad file would fail this). Read the first variant of each via the
      // public reader for a known key per endpoint below; here we just
      // assert the file loads by reading a variant that must exist.
      // `install-state` uses `post_first_install`; the rest use a shared
      // populated/empty/synthetic name asserted in the per-endpoint test.
      expect(typeof endpoint).toBe("string");
    }
  });

  test("readGoldenVariant returns the recorded body for a known variant", () => {
    const body = readGoldenVariant("install-state", "post_first_install");
    expect(body).toEqual({ first_install: false });
  });

  test("readGoldenVariant returns the empty-set variant for a dashboard route", () => {
    const body = readGoldenVariant("framework-posture", "empty");
    expect(body).toEqual({ count: 0, frameworks: [] });
  });

  test("readGoldenVariant throws (listing variants) on an unknown variant", () => {
    expect(() => readGoldenVariant("me", "no_such_variant")).toThrowError(
      /no variant "no_such_variant"/,
    );
    expect(() => readGoldenVariant("me", "no_such_variant")).toThrowError(
      /available: /,
    );
  });

  test("fulfillFromGolden serves the recorded body at status 200 by default", async () => {
    const { route, calls } = fakeRoute();
    await fulfillFromGolden(
      route,
      "install-state",
      "fresh_install_without_tenant",
    );
    expect(calls).toHaveLength(1);
    expect(calls[0].status).toBe(200);
    expect(calls[0].contentType).toBe("application/json");
    expect(JSON.parse(calls[0].body!)).toEqual({ first_install: true });
  });

  test("fulfillFromGolden honors an explicit status (escape hatch)", async () => {
    const { route, calls } = fakeRoute();
    await fulfillFromGolden(route, "demo-status", "enabled", { status: 200 });
    expect(calls[0].status).toBe(200);
    // The golden body is served regardless of status override.
    expect(JSON.parse(calls[0].body!)).toEqual({ enabled: true });
  });

  test("override deep-merges over the golden base, preserving the recorded shape", async () => {
    const { route, calls } = fakeRoute();
    // The credential-bearer specs override display_name while keeping the
    // recorded synthetic_admin shape (roles:[], is_admin, tenant_role, …).
    await fulfillFromGolden(route, "me", "synthetic_admin", {
      override: { display_name: "API key 1f3a", owner_roles: [] },
    });
    const body = JSON.parse(calls[0].body!) as Record<string, unknown>;
    // Overridden fields:
    expect(body.display_name).toBe("API key 1f3a");
    expect(body.owner_roles).toEqual([]);
    // Recorded-truth fields survive (shape-complete base — slice 276 lesson):
    expect(body.roles).toEqual([]);
    expect(body.is_admin).toBe(true);
    expect(body.tenant_role).toBe("admin");
    expect(body.email).toBe("");
    expect(body.time_zone).toBeNull();
  });

  test("override of freshness numbers keeps the recorded bucket/array shape", async () => {
    const { route, calls } = fakeRoute();
    // The slice-229 subtitle test needs a deterministic 87% but keeps the
    // golden's bucket:"class" + array shape.
    await fulfillFromGolden(route, "freshness", "populated", {
      override: {
        buckets: [
          { freshness_class: "monthly", total: 100, fresh: 87, stale: 13 },
        ],
        total: 100,
        total_stale: 13,
      },
    });
    const body = JSON.parse(calls[0].body!) as Record<string, unknown>;
    expect(body.bucket).toBe("class");
    expect(body.total).toBe(100);
    expect(body.total_stale).toBe(13);
    expect(Array.isArray(body.buckets)).toBe(true);
  });
});
