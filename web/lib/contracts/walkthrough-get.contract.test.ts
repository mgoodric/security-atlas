// Slice 689 — contract-test-tier rollout (consumer side: GET
// /v1/walkthroughs/{id}, the audit-workspace single-walkthrough read with
// attachments + canonical_hash + tamper flag).
//
// PROVIDER: internal/api/walkthroughs/contractrecord_test.go records the real
// Get handler's bodies into walkthrough-get.golden.json. This CONSUMER half
// asserts the BFF (web/app/api/audit/walkthroughs/[id]/route.ts) against them.
// The BFF is a VERBATIM passthrough: forwardJSON forwards the upstream body
// text unchanged — so the assert is toEqual(golden).
//
// Load-bearing field assumptions:
//   * walkthrough is an object carrying string id/control_id/narrative/status
//   * canonical_hash is a hex string; tamper_detected is always a boolean (AC-6)
//   * attachments + audit_period_id + transcript are omitempty; when present,
//     each attachment carries string id/storage_key/content_type/sha256 + a
//     number size_bytes + opaque annotations JSON

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

import { GET } from "../../app/api/audit/walkthroughs/[id]/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "walkthrough-get.golden.json"), "utf8"),
) as Golden;

const WALKTHROUGH_ID = "22222222-2222-4222-8222-222222222222";
const ctx = { params: Promise.resolve({ id: WALKTHROUGH_ID }) };

describe("contract: GET /api/audit/walkthroughs/[id] <-> atlas GET /v1/walkthroughs/{id}", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/walkthroughs/{id}");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      const wt = body.walkthrough as Record<string, unknown>;
      expect(typeof wt, `${name}.walkthrough`).toBe("object");
      expect(typeof wt.id, `${name}.walkthrough.id`).toBe("string");
      expect(typeof wt.control_id, `${name}.walkthrough.control_id`).toBe(
        "string",
      );
      expect(typeof wt.narrative, `${name}.walkthrough.narrative`).toBe(
        "string",
      );
      expect(typeof wt.status, `${name}.walkthrough.status`).toBe("string");
      expect(typeof wt.canonical_hash, `${name}.canonical_hash`).toBe("string");
      expect(typeof wt.tamper_detected, `${name}.tamper_detected`).toBe(
        "boolean",
      );
      if (wt.attachments !== undefined) {
        expect(
          Array.isArray(wt.attachments),
          `${name}.attachments must be an array when present`,
        ).toBe(true);
        for (const a of wt.attachments as Record<string, unknown>[]) {
          expect(typeof a.id, `${name}.attachment.id`).toBe("string");
          expect(typeof a.storage_key, `${name}.attachment.storage_key`).toBe(
            "string",
          );
          expect(typeof a.content_type, `${name}.attachment.content_type`).toBe(
            "string",
          );
          expect(typeof a.sha256, `${name}.attachment.sha256`).toBe("string");
          expect(typeof a.size_bytes, `${name}.attachment.size_bytes`).toBe(
            "number",
          );
        }
      }
    }
  });

  test("canonical_hash is 64 lowercase hex chars", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      const wt = body.walkthrough as Record<string, unknown>;
      expect(wt.canonical_hash as string, `${name}.canonical_hash`).toMatch(
        /^[0-9a-f]{64}$/,
      );
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

      const res = await GET({} as never, ctx);
      expect(res.status).toBe(200);
      const got = (await res.json()) as Record<string, unknown>;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    const res = await GET({} as never, ctx);
    expect(res.status).toBe(401);
  });
});
