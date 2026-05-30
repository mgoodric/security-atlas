// Slice 392 — contract-test-tier ROLLOUT (consumer side: GET /v1/admin/demo/status).
//
// The PROVIDER half (internal/api/admindemo/status_contract_test.go)
// records the real Go handler's GET /v1/admin/demo/status response bodies
// into demo-status.golden.json. This CONSUMER half asserts the Next.js
// BFF (web/app/api/admin/demo/status/route.ts) against those recorded
// bodies, extending the slice-349 pilot pattern (ADR-0007) to this
// admin-gated endpoint.
//
// The BFF reads the session cookie, calls getAdminDemoStatus(bearer)
// (web/lib/api.ts) which fetches the upstream and returns the parsed
// {enabled} body, then re-serializes it. So the consumer assertion is
// that the BFF emits the recorded {enabled: boolean} shape unchanged.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";

import { GET } from "../../app/api/admin/demo/status/route";

interface DemoStatusGolden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: DemoStatusGolden = JSON.parse(
  readFileSync(join(__dirname, "demo-status.golden.json"), "utf8"),
) as DemoStatusGolden;

describe("contract: GET /api/admin/demo/status <-> atlas GET /v1/admin/demo/status", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/admin/demo/status");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant carries a boolean enabled (BFF assumption)", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        typeof body.enabled,
        `variant ${name} must carry boolean enabled`,
      ).toBe("boolean");
    }
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
      const providerBody = golden.variants[variantName];
      vi.spyOn(globalThis, "fetch").mockImplementation(
        async () =>
          new Response(JSON.stringify(providerBody), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );

      const res = await GET();
      expect(res.status).toBe(200);
      const got = (await res.json()) as Record<string, unknown>;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    const res = await GET();
    expect(res.status).toBe(401);
  });
});
