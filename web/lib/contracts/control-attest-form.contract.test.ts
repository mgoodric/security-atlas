// Slice 692 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/attest-form, the manual-attestation form descriptor the
// control-detail attest surface renders).
//
// PROVIDER: internal/api/controls/attest_contract_test.go records the real
// AttestForm handler's bodies into control-attest-form.golden.json. This
// CONSUMER half asserts the BFF (web/app/api/controls/[id]/attest-form/route.ts)
// against them. The BFF is a VERBATIM passthrough: getAttestForm
// (web/lib/api/attest.ts) returns res.json() unchanged and the route does
// NextResponse.json(form) — so the assert is toEqual(golden).
//
// Load-bearing field assumptions (AttestForm type in web/lib/api/attest.ts):
//   * control_id / bundle_id / title / implementation_type / owner_role /
//     platform_schema_kind / platform_schema_version are strings
//   * implementation_type is one of manual_attested | manual_periodic
//   * manual_evidence_schema is an object (the control bundle's per-control
//     JSON Schema declaration) — opaque to the BFF
//   * caller_can_attest is a bool — true when the credential holds the
//     control's owner_role; drives the attest button's enabled state
//   * platform_schema_requires is a string array
//   * freshness_class is string-or-null (omitempty)

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

import { GET } from "../../app/api/controls/[id]/attest-form/route";

interface AttestFormBody {
  control_id: string;
  bundle_id: string;
  title: string;
  implementation_type: string;
  owner_role: string;
  freshness_class: string | null;
  manual_evidence_schema: Record<string, unknown>;
  caller_can_attest: boolean;
  platform_schema_kind: string;
  platform_schema_version: string;
  platform_schema_requires: string[];
}

interface Golden {
  endpoint: string;
  variants: Record<string, AttestFormBody>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-attest-form.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };
const ALLOWED_IMPL = new Set(["manual_attested", "manual_periodic"]);

describe("contract: GET /api/controls/[id]/attest-form <-> atlas GET /v1/controls/{id}/attest-form", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/attest-form");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every variant satisfies the AttestForm field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const field of [
        "control_id",
        "bundle_id",
        "title",
        "implementation_type",
        "owner_role",
        "platform_schema_kind",
        "platform_schema_version",
      ] as const) {
        expect(typeof body[field], `${name}.${field}`).toBe("string");
      }
      expect(
        ALLOWED_IMPL.has(body.implementation_type),
        `${name}.implementation_type '${body.implementation_type}' is manual`,
      ).toBe(true);
      expect(typeof body.caller_can_attest, `${name}.caller_can_attest`).toBe(
        "boolean",
      );
      expect(
        Array.isArray(body.platform_schema_requires),
        `${name}.platform_schema_requires is array`,
      ).toBe(true);
      for (const req of body.platform_schema_requires) {
        expect(typeof req, `${name}.platform_schema_requires[]`).toBe("string");
      }
      // manual_evidence_schema is an opaque object.
      expect(
        typeof body.manual_evidence_schema,
        `${name}.manual_evidence_schema`,
      ).toBe("object");
      expect(
        body.manual_evidence_schema,
        `${name}.manual_evidence_schema not null`,
      ).not.toBeNull();
      // freshness_class is string-or-null (never absent in a manual form).
      expect("freshness_class" in body, `${name}.freshness_class`).toBe(true);
      if (body.freshness_class !== null) {
        expect(typeof body.freshness_class, `${name}.freshness_class`).toBe(
          "string",
        );
      }
    }
  });

  test("the two variants pin both branches of caller_can_attest", () => {
    const owner = golden.variants.owner;
    const viewer = golden.variants.viewer;
    expect(owner, "owner variant present").toBeDefined();
    expect(viewer, "viewer variant present").toBeDefined();
    expect(owner.caller_can_attest, "owner can attest").toBe(true);
    expect(viewer.caller_can_attest, "viewer cannot attest").toBe(false);
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-692");
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
      const got = (await res.json()) as AttestFormBody;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    const res = await GET({} as never, ctx);
    expect(res.status).toBe(401);
  });
});
