// #2106 — vitest coverage for the risk control-link BFF routes: link
// (POST) and unlink (DELETE). Each rejects a non-UUID id before any
// upstream call, forwards to /v1/risks/{id}/controls[/...] with the cookie
// bearer, and passes the upstream status + body through (the 400 / 409
// refusals the page shows).

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
import { DELETE } from "./[controlId]/route";
import { POST } from "./route";

const RISK = "11111111-1111-1111-1111-111111111111";
const CONTROL = "22222222-2222-2222-2222-222222222222";

function params<T>(p: T) {
  return { params: Promise.resolve(p) };
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

function post(body: string) {
  return new Request("http://x", { method: "POST", body });
}

describe("risk control-link BFF routes (#2106)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("POST forwards the control id with the bearer", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { risk: { id: RISK } });

    const res = await POST(
      post(JSON.stringify({ control_id: CONTROL, extra: "dropped" })),
      params({ id: RISK }),
    );

    expect(res.status).toBe(200);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(calls[0].url).toBe(`http://atlas:8080/v1/risks/${RISK}/controls`);
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      control_id: CONTROL,
    });
    const headers = calls[0].init?.headers as Record<string, string>;
    expect(headers.Authorization).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });

  test("POST passes a 409 closed-risk refusal through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    captureFetch(409, { error: "the risk is closed; reopen it first" });

    const res = await POST(
      post(JSON.stringify({ control_id: CONTROL })),
      params({ id: RISK }),
    );

    expect(res.status).toBe(409);
    expect(((await res.json()) as { error: string }).error).toContain("closed");
  });

  test.each([
    ["a non-UUID risk id", "not-a-uuid", { control_id: CONTROL }, "id"],
    ["a non-UUID control id", RISK, { control_id: "../../x" }, "control_id"],
    ["a missing control id", RISK, {}, "control_id"],
  ])(
    "POST with %s is a 400 without an upstream call",
    async (_name, id, body, field) => {
      cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
      const fetchSpy = vi.spyOn(globalThis, "fetch");

      const res = await POST(post(JSON.stringify(body)), params({ id }));

      expect(res.status).toBe(400);
      expect(((await res.json()) as { error: string }).error).toBe(
        `${field} must be a UUID`,
      );
      expect(fetchSpy).not.toHaveBeenCalled();
    },
  );

  test("POST with a malformed body is a 400 without an upstream call", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    const res = await POST(post("{not json"), params({ id: RISK }));

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("DELETE forwards to the upstream unlink", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const calls = captureFetch(200, { risk: { id: RISK } });

    const res = await DELETE(
      new Request("http://x", { method: "DELETE" }),
      params({ id: RISK, controlId: CONTROL }),
    );

    expect(res.status).toBe(200);
    expect(calls[0].url).toBe(
      `http://atlas:8080/v1/risks/${RISK}/controls/${CONTROL}`,
    );
    expect(calls[0].init?.method).toBe("DELETE");
  });

  test("DELETE passes the last-control 400 through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    captureFetch(400, {
      error:
        "risk: treatment validation: treatment=mitigate requires at least one linked control or linked action plan",
    });

    const res = await DELETE(
      new Request("http://x", { method: "DELETE" }),
      params({ id: RISK, controlId: CONTROL }),
    );

    expect(res.status).toBe(400);
  });

  test.each([
    ["risk", "nope", CONTROL, "id"],
    ["control", RISK, "nope", "control_id"],
  ])(
    "DELETE with a non-UUID %s id is a 400 without an upstream call",
    async (_name, id, controlId, field) => {
      cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
      const fetchSpy = vi.spyOn(globalThis, "fetch");

      const res = await DELETE(
        new Request("http://x", { method: "DELETE" }),
        params({ id, controlId }),
      );

      expect(res.status).toBe(400);
      expect(((await res.json()) as { error: string }).error).toBe(
        `${field} must be a UUID`,
      );
      expect(fetchSpy).not.toHaveBeenCalled();
    },
  );

  test("no session cookie is a 401 without an upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    const res = await DELETE(
      new Request("http://x", { method: "DELETE" }),
      params({ id: RISK, controlId: CONTROL }),
    );

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
