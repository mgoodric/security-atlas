// Slice 392 — contract-test-tier ROLLOUT (consumer side: GET /v1/me).
//
// The PROVIDER half (internal/api/me/profile_contract_test.go) records
// the real Go handler's GET /v1/me response bodies into me.golden.json.
// This CONSUMER half asserts the Next.js BFF (web/app/api/me/route.ts)
// against those recorded bodies, extending the slice-349 pilot pattern
// (ADR-0007) to the identity/profile surface — the slice-210-class
// surface with the highest consumer coupling.
//
// The me BFF reads the session cookie, fetches the upstream, and passes
// the parsed JSON through verbatim (passthrough()). So the consumer
// assertion is total: the BFF must emit exactly the recorded body.
//
// Load-bearing field assumptions the BFF + frontend rely on:
//   * user_id / tenant_id / is_admin / tenant_role always present
//   * roles is always an array (never null) — the slice-130 contract
//   * owner_roles is an array OR null (the synthetic-admin path emits
//     null when no owner roles are granted; the frontend treats
//     null === [])
//   * time_zone is nullable (string | null)

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

import { SESSION_COOKIE } from "@/lib/auth";

import { GET } from "../../app/api/me/route";

interface MeGolden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: MeGolden = JSON.parse(
  readFileSync(join(__dirname, "me.golden.json"), "utf8"),
) as MeGolden;

describe("contract: GET /api/me <-> atlas GET /v1/me", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/me");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.user_id, `${name}.user_id`).toBe("string");
      expect(typeof body.tenant_id, `${name}.tenant_id`).toBe("string");
      expect(typeof body.is_admin, `${name}.is_admin`).toBe("boolean");
      expect(typeof body.tenant_role, `${name}.tenant_role`).toBe("string");
      // roles is always an array (slice-130 contract: "[]" never null).
      expect(Array.isArray(body.roles), `${name}.roles must be an array`).toBe(
        true,
      );
      // owner_roles is an array OR null (the synthetic path emits null
      // when empty); the frontend treats null === [].
      expect(
        Array.isArray(body.owner_roles) || body.owner_roles === null,
        `${name}.owner_roles must be an array or null`,
      ).toBe(true);
      // time_zone is nullable string.
      expect(
        typeof body.time_zone === "string" || body.time_zone === null,
        `${name}.time_zone must be string or null`,
      ).toBe(true);
    }
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      cookieStore.set(SESSION_COOKIE, "test-bearer");
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
