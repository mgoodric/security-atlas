// Slice 392 — contract-test-tier ROLLOUT (consumer side: GET /v1/version).
//
// The PROVIDER half (internal/api/version_contract_test.go) records the
// real Go handler's GET /v1/version response bodies into
// version.golden.json. This CONSUMER half asserts the Next.js BFF
// (web/app/api/version/route.ts) against those recorded bodies — closing
// the silent mock-vs-reality gap that Q-1 (slice 333) / P-1 (slice 334)
// named, extending the slice-349 pilot pattern (ADR-0007) to this
// endpoint.
//
// The version BFF is a verbatim passthrough: it forwards the upstream
// body and status unchanged (only the Cache-Control header is this
// layer's own). So the consumer assertion is total — the BFF must emit
// exactly what the provider recorded.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

import { GET } from "../../app/api/version/route";

interface VersionGolden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: VersionGolden = JSON.parse(
  readFileSync(join(__dirname, "version.golden.json"), "utf8"),
) as VersionGolden;

// The four fields the BFF proxy + VersionFooter (web/lib/version.ts)
// rely on. They are non-pointer strings on the Go side, so every variant
// MUST carry all four (empty string, never absent).
const REQUIRED_FIELDS = [
  "version",
  "commit",
  "build_time",
  "go_version",
] as const;

describe("contract: GET /api/version <-> atlas GET /v1/version", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/version");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant carries the four string fields", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const field of REQUIRED_FIELDS) {
        expect(
          typeof body[field],
          `variant ${name} must carry string ${field}`,
        ).toBe("string");
      }
    }
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
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
      // The version BFF sets a public cache header but does not mutate
      // the body — verbatim passthrough.
      expect(res.headers.get("Cache-Control")).toBe("public, max-age=300");
      const got = (await res.json()) as Record<string, unknown>;
      expect(got).toEqual(providerBody);
    });
  }
});
