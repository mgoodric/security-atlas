// Slice 253 — vitest coverage for the per-control risks BFF proxy.
//
// Pattern mirrors `app/api/controls/[id]/policies/route.test.ts` for the
// peer four-class coverage: 401 / 200 / 404 / 5xx. The risks endpoint
// surfaces opaque `inherent_score` / `residual_score` JSON blobs (canvas
// §2.2) plus a numeric `link_weight`; the page renders the residual
// magnitude via the slice-067 `formatResidualScore` helper.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { GET } from "./route";

const CONTROL_ID = "33333333-3333-3333-3333-333333330001";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/controls/[id]/risks", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when atlas_jwt cookie is absent", async () => {
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/risks`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer to upstream and passes 200 through", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-253");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          control_id: CONTROL_ID,
          risks: [
            {
              risk_id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
              title: "Credential theft via phishing",
              inherent_score: { likelihood: 4, impact: 5 },
              residual_score: { likelihood: 2, impact: 3 },
              link_weight: 0.7,
            },
          ],
          count: 1,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/risks`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      risks: unknown[];
      count: number;
    };
    expect(body.count).toBe(1);
    expect(body.risks).toHaveLength(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(`/v1/controls/${CONTROL_ID}/risks`);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-253");
  });

  test("propagates upstream 404 (control resolves but is unknown in this tenant)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-253");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "control not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/risks`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(404);
  });

  test("propagates upstream 5xx (downstream-platform failure)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-253");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/risks`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(502);
  });
});
