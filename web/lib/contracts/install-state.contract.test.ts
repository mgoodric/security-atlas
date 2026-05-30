// Slice 349 — contract-test-tier PILOT (consumer side).
//
// The PROVIDER half (internal/api/install_state_contract_test.go)
// records the real Go handler's GET /v1/install-state response bodies
// into install-state.golden.json. This CONSUMER half asserts the
// Next.js BFF (web/app/api/install-state/route.ts) against those
// recorded bodies — closing the silent mock-vs-reality gap that Q-1
// (slice 333) and P-1 (slice 334) named.
//
// What this catches that the existing surfaces do NOT:
//   * The existing route.test.ts hand-writes `{ first_install: true }`
//     as the upstream mock. Nothing ties that literal to the real Go
//     shape. If the handler renamed `first_install` -> `firstInstall`,
//     the route test stays green on its own invented mock; only this
//     contract test (reading the provider-recorded golden) fails.
//   * The slice 140 openapi-drift check verifies route PRESENCE +
//     auth tier + structural shape, but explicitly NOT response-body
//     schemas (see internal/api/openapi/validator.go "What it does NOT
//     check"). The body shape is exactly this tier's surface.
//
// This is a deterministic, dependency-free test on the existing vitest
// surface. No Pact broker, no schemathesis, no new tooling.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

import { GET } from "../../app/api/install-state/route";

interface InstallStateGolden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: InstallStateGolden = JSON.parse(
  readFileSync(join(__dirname, "install-state.golden.json"), "utf8"),
) as InstallStateGolden;

// The contract the BFF relies on: every fresh-install variant carries a
// boolean `first_install`, and the BFF's 5xx fallback synthesizes
// `{ first_install: false }` — so `first_install` MUST exist on every
// provider variant for the consumer's mental model to hold.
describe("contract: GET /api/install-state <-> atlas GET /v1/install-state", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/install-state");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant carries a boolean first_install (BFF assumption)", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        typeof body.first_install,
        `variant ${name} must carry boolean first_install`,
      ).toBe("boolean");
    }
  });

  // Drive the real BFF with each recorded provider body and assert the
  // BFF passes the success shape through verbatim. If the provider shape
  // drifts (new/renamed field), the golden regenerates on the Go side
  // and this loop re-validates the BFF against the NEW truth in one PR.
  for (const variantName of Object.keys({
    fresh_install_with_tenant: 1,
    fresh_install_without_tenant: 1,
    post_first_install: 1,
  })) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      const providerBody = golden.variants[variantName];
      expect(
        providerBody,
        `golden is missing variant ${variantName} — provider and consumer drifted; run the Go recorder with -update`,
      ).toBeDefined();

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
      // Verbatim passthrough: the BFF must not drop or rename any field
      // the provider emitted on the success path.
      expect(got).toEqual(providerBody);
    });
  }

  test("BFF 5xx fallback shape is a subset the provider also satisfies", async () => {
    // The BFF synthesizes { first_install: false } on upstream failure.
    // That synthesized shape must be assignable to the provider contract
    // (i.e. first_install:boolean), which the post_first_install variant
    // proves.
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async () => new Response("upstream boom", { status: 503 }),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const got = (await res.json()) as { first_install?: unknown };
    expect(typeof got.first_install).toBe("boolean");
    expect(got.first_install).toBe(false);
  });
});
