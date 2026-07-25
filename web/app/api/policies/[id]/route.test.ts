// Slice 672 — vitest coverage for the `/api/policies/[id]` read-only
// detail BFF proxy.
//
// Mirrors the slice 253 `app/api/controls/[id]/policies/route.test.ts`
// per-id-with-bearer shape and the slice 101 list-route test. The route
// guarantees (invariant #6 — cookie session is the only tenant context;
// NO client tenant_id):
//   * 401 when the atlas_jwt cookie is absent
//   * forwards the bearer to `GET /v1/policies/{id}` (no tenant_id param)
//   * non-published policy → ONE upstream call, body passed through
//   * published policy → policy + ack-rate composed into one response
//   * upstream 404 → 404 { error: "policy not found" } (page -> notFound)
//   * upstream 5xx → propagated (clean error line, no internal leak)
//
// No vendor-prefixed tokens in fixtures (P0-A4 — slice 098 / 141 norm).

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { GET } from "./route";

const POLICY_ID = "00000000-0000-0000-0000-000000000001";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

function draftPolicyBody() {
  return JSON.stringify({
    policy: {
      id: POLICY_ID,
      title: "Information Security Policy",
      version: "v1.0",
      body_md: "# Heading\n\nbody",
      owner_role: "security_lead",
      approver_role: "cto",
      linked_control_ids: [],
      acknowledgment_required_roles: ["all_staff"],
      status: "draft",
      source_attribution: "in_house",
      created_by: "user-1",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-04-22T00:00:00Z",
    },
  });
}

function publishedPolicyBody() {
  return JSON.stringify({
    policy: {
      id: POLICY_ID,
      title: "Information Security Policy",
      version: "v3.2",
      body_md: "# Heading\n\nbody",
      owner_role: "security_lead",
      approver_role: "cto",
      linked_control_ids: [],
      acknowledgment_required_roles: ["all_staff"],
      status: "published",
      source_attribution: "in_house",
      created_by: "user-1",
      published_at: "2026-01-15T00:00:00Z",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-04-22T00:00:00Z",
    },
  });
}

describe("GET /api/policies/[id]", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when atlas_jwt cookie is absent", async () => {
    const req = new Request(`http://localhost/api/policies/${POLICY_ID}`);
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(POLICY_ID),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer to /v1/policies/{id} and passes a draft policy through (one call, no ack-rate)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-672");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(draftPolicyBody(), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const req = new Request(`http://localhost/api/policies/${POLICY_ID}`);
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(POLICY_ID),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      policy: { id: string; status: string };
      ack_rate: unknown;
    };
    expect(body.policy.id).toBe(POLICY_ID);
    // Non-published policy never queries the ack-rate endpoint.
    expect(body.ack_rate).toBeNull();
    expect(fetchSpy).toHaveBeenCalledOnce();

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(`/v1/policies/${POLICY_ID}`);
    // Invariant #6: NO client-supplied tenant_id forwarded upstream.
    expect(calledURL).not.toContain("tenant_id");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-672");
  });

  test("composes ack-rate for a published policy (two calls)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-672");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(publishedPolicyBody(), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            numerator: 8,
            denominator: 10,
            percent: 80,
            window_seconds: 31536000,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
    const req = new Request(`http://localhost/api/policies/${POLICY_ID}`);
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(POLICY_ID),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      ack_rate: { numerator: number; percent: number } | null;
    };
    expect(body.ack_rate?.numerator).toBe(8);
    expect(body.ack_rate?.percent).toBe(80);
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    const ackURL = String(fetchSpy.mock.calls[1]?.[0] ?? "");
    expect(ackURL).toContain(`/v1/policies/${POLICY_ID}/acknowledgment-rate`);
  });

  test("degrades gracefully when ack-rate upstream 409s (published-but-unavailable)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-672");
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(publishedPolicyBody(), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(new Response("conflict", { status: 409 }));
    const req = new Request(`http://localhost/api/policies/${POLICY_ID}`);
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(POLICY_ID),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as { ack_rate: unknown };
    expect(body.ack_rate).toBeNull();
  });

  test("maps upstream 404 to a clean 404 { error: policy not found }", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-672");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "policy not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const req = new Request(`http://localhost/api/policies/${POLICY_ID}`);
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(POLICY_ID),
    );
    expect(res.status).toBe(404);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("policy not found");
  });

  test("propagates upstream 5xx without leaking an internal error", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-672");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("boom: internal stack trace", { status: 502 }),
    );
    const req = new Request(`http://localhost/api/policies/${POLICY_ID}`);
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(POLICY_ID),
    );
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error?: string };
    // The clean APIError status line — NOT the raw upstream body.
    expect(body.error).not.toContain("stack trace");
  });
});
