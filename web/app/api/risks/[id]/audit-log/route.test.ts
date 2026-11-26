// #2104 — vitest coverage for the risk edit + history BFF routes:
// PATCH /api/risks/[id] and GET /api/risks/[id]/audit-log. Each forwards
// to the matching /v1/risks/{id} upstream with the cookie bearer and
// passes the upstream status + body through, including the 400 the
// platform returns for a treatment field.

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
import { PATCH } from "../route";
import { GET } from "./route";

const RISK = "11111111-1111-1111-1111-111111111111";

function params(id: string) {
  return { params: Promise.resolve({ id }) };
}

function captureFetch(status: number, body: unknown) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init });
      return new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    },
  );
  return calls;
}

describe("risk edit + history BFF routes (#2104)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("PATCH forwards the body to the upstream risk with the bearer", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { risk: { id: RISK, title: "New" } });

    const req = new Request("http://x", {
      method: "PATCH",
      body: JSON.stringify({ title: "New" }),
    });
    const res = await PATCH(req as never, params(RISK));

    expect(res.status).toBe(200);
    expect(calls[0].url).toBe(`http://atlas:8080/v1/risks/${RISK}`);
    expect(calls[0].init?.method).toBe("PATCH");
    expect(calls[0].init?.body).toBe(JSON.stringify({ title: "New" }));
    expect(
      (calls[0].init?.headers as Record<string, string>).Authorization,
    ).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });

  test("PATCH forwards If-Match and passes the upstream 412 through (#2311)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(412, {
      error: "risk changed since it was read",
    });
    const tag = '"2026-11-17T19:11:33.123456Z"';

    const req = new Request("http://x", {
      method: "PATCH",
      headers: { "If-Match": tag },
      body: JSON.stringify({ title: "New" }),
    });
    const res = await PATCH(req as never, params(RISK));

    expect(res.status).toBe(412);
    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers["If-Match"]).toBe(tag);
    expect(headers.Authorization).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });

  test("PATCH without If-Match forwards none", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { risk: { id: RISK } });

    const req = new Request("http://x", {
      method: "PATCH",
      body: JSON.stringify({ title: "New" }),
    });
    await PATCH(req as never, params(RISK));

    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers["If-Match"]).toBeUndefined();
  });

  test("PATCH passes an upstream 400 through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const msg = "treatment cannot be changed by PATCH /v1/risks/{id}";
    captureFetch(400, { error: msg });

    const req = new Request("http://x", {
      method: "PATCH",
      body: JSON.stringify({ treatment: "avoid" }),
    });
    const res = await PATCH(req as never, params(RISK));

    expect(res.status).toBe(400);
    expect(((await res.json()) as { error: string }).error).toBe(msg);
  });

  test("PATCH refuses a non-JSON body without calling upstream", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, {});
    const req = new Request("http://x", { method: "PATCH", body: "nope" });
    const res = await PATCH(req as never, params(RISK));
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  test("GET audit-log forwards to the upstream history, never cached", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { entries: [], count: 0 });

    const res = await GET(new Request("http://x"), params(RISK));

    expect(res.status).toBe(200);
    expect(calls[0].url).toBe(`http://atlas:8080/v1/risks/${RISK}/audit-log`);
    expect(res.headers.get("Cache-Control")).toContain("no-store");
  });

  test("GET audit-log is 401 without the cookie", async () => {
    const calls = captureFetch(200, {});
    const res = await GET(new Request("http://x"), params(RISK));
    expect(res.status).toBe(401);
    expect(calls).toHaveLength(0);
  });
});
