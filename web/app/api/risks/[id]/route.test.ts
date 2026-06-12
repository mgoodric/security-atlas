// Slice 681 / ATLAS-039 — vitest coverage for the `/api/risks/[id]`
// read-only detail BFF proxy.
//
// Mirrors the slice 672 `app/api/policies/[id]/route.test.ts` shape. The
// route guarantees (invariant #6 — the cookie session is the ONLY tenant
// context; NO client tenant_id):
//   * 401 when the atlas_jwt cookie is absent
//   * forwards the bearer to `GET /v1/risks/{id}` (no tenant_id param)
//   * happy path: `{ risk, residual? }` passed through verbatim
//   * upstream 404 -> 404 { error: "risk not found" } (page -> notFound)
//   * upstream 401 -> 401 propagated (page redirects to /login)
//   * upstream 5xx -> propagated (clean error line, no internal leak)
//
// Neutral fixtures only (P0-A4 — no vendor-prefixed tokens).

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

const RISK_ID = "00000000-0000-0000-0000-000000000001";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

function riskBody() {
  return JSON.stringify({
    risk: {
      id: RISK_ID,
      title: "sample operational risk",
      description: "a neutral description",
      category: "operational",
      methodology: "qualitative_5x5",
      inherent_score: { likelihood: 4, impact: 5 },
      treatment: "mitigate",
      treatment_owner: "owner-a",
      residual_score: { likelihood: 2, impact: 3 },
      review_due_at: "2026-09-01",
      accepted_until: null,
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-04-22T00:00:00Z",
      themes: [],
      severity: 20,
    },
    residual: { magnitude: 0.24, effectiveness: 0.6 },
  });
}

const realFetch = global.fetch;

beforeEach(() => {
  cookieStore.clear();
  vi.restoreAllMocks();
  global.fetch = realFetch;
});

describe("GET /api/risks/[id]", () => {
  test("401 when the bearer cookie is absent", async () => {
    const res = await GET({} as never, paramsFor(RISK_ID));
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards the bearer to GET /v1/risks/{id} (no tenant_id) and passes through", async () => {
    cookieStore.set("atlas_jwt", "neutral-bearer");
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      expect(url).toContain(`/v1/risks/${RISK_ID}`);
      // Invariant #6: no tenant_id is ever appended to the upstream URL.
      expect(url).not.toContain("tenant_id");
      const auth = (init?.headers as Record<string, string>)?.Authorization;
      expect(auth).toBe("Bearer neutral-bearer");
      return new Response(riskBody(), { status: 200 });
    });
    global.fetch = fetchMock as unknown as typeof fetch;

    const res = await GET({} as never, paramsFor(RISK_ID));
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      risk: { id: string; severity: number };
      residual: { magnitude: number };
    };
    expect(body.risk.id).toBe(RISK_ID);
    expect(body.risk.severity).toBe(20);
    expect(body.residual.magnitude).toBe(0.24);
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  test("upstream 404 -> 404 { error: 'risk not found' }", async () => {
    cookieStore.set("atlas_jwt", "neutral-bearer");
    global.fetch = vi.fn(
      async () => new Response("not found", { status: 404 }),
    ) as unknown as typeof fetch;

    const res = await GET({} as never, paramsFor(RISK_ID));
    expect(res.status).toBe(404);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("risk not found");
  });

  test("upstream 401 -> 401 propagated", async () => {
    cookieStore.set("atlas_jwt", "neutral-bearer");
    global.fetch = vi.fn(
      async () => new Response("unauthorized", { status: 401 }),
    ) as unknown as typeof fetch;

    const res = await GET({} as never, paramsFor(RISK_ID));
    expect(res.status).toBe(401);
  });

  test("upstream 5xx -> propagated with a clean error line", async () => {
    cookieStore.set("atlas_jwt", "neutral-bearer");
    global.fetch = vi.fn(
      async () => new Response("boom", { status: 500 }),
    ) as unknown as typeof fetch;

    const res = await GET({} as never, paramsFor(RISK_ID));
    expect(res.status).toBe(500);
    const body = (await res.json()) as { error: string };
    // The error line is the status line, NOT the upstream body ("boom").
    expect(body.error).not.toContain("boom");
  });
});
