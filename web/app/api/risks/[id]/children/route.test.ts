// #1748 — vitest coverage for the risk hierarchy BFF routes: children
// (GET, POST), detach (DELETE) and parents (GET). Each forwards to the
// matching /v1/risks/{id}/... upstream with the cookie bearer, passes the
// upstream status + body through (including the 400 / 409 refusals the
// page shows), and never browser-caches the listing.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../../../../lib/test-utils/test-tokens";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { DELETE } from "./[childId]/route";
import { GET as GET_PARENTS } from "../parents/route";
import { GET, POST } from "./route";

const PARENT = "11111111-1111-1111-1111-111111111111";
const CHILD = "22222222-2222-2222-2222-222222222222";

function params<T>(p: T) {
  return { params: Promise.resolve(p) };
}

function captureFetch(status: number, body: unknown) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init });
      return new Response(status === 204 ? null : JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    },
  );
  return calls;
}

describe("risk hierarchy BFF routes (#1748)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("GET children forwards to the upstream listing with the bearer", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { children: [], count: 0 });

    const res = await GET(new Request("http://x"), params({ id: PARENT }));

    expect(res.status).toBe(200);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(calls[0].url).toBe(`http://atlas:8080/v1/risks/${PARENT}/children`);
    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers.Authorization).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });

  test("POST children forwards the body and passes a 409 cycle refusal through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(409, { error: "link would create a cycle" });

    const res = await POST(
      new Request("http://x", {
        method: "POST",
        body: JSON.stringify({ child_risk_ids: [CHILD] }),
      }),
      params({ id: PARENT }),
    );

    expect(res.status).toBe(409);
    expect(((await res.json()) as { error: string }).error).toContain("cycle");
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      child_risk_ids: [CHILD],
    });
  });

  test("POST children with a malformed body is a 400 without an upstream call", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    const res = await POST(
      new Request("http://x", { method: "POST", body: "{not json" }),
      params({ id: PARENT }),
    );

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("DELETE child forwards to the upstream detach and passes the 204 through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(204, null);

    const res = await DELETE(
      new Request("http://x", { method: "DELETE" }),
      params({ id: PARENT, childId: CHILD }),
    );

    expect(res.status).toBe(204);
    expect(calls[0].url).toBe(
      `http://atlas:8080/v1/risks/${PARENT}/children/${CHILD}`,
    );
    expect(calls[0].init?.method).toBe("DELETE");
  });

  test("GET parents forwards to the upstream parents listing", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { parents: [], count: 0 });

    const res = await GET_PARENTS(
      new Request("http://x"),
      params({ id: CHILD }),
    );

    expect(res.status).toBe(200);
    expect(calls[0].url).toBe(`http://atlas:8080/v1/risks/${CHILD}/parents`);
  });

  test("no session cookie is a 401 without an upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    const res = await GET(new Request("http://x"), params({ id: PARENT }));

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
