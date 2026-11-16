// #2116 — vitest coverage for the policy write BFF routes: POST
// /api/policies (create), PATCH and DELETE /api/policies/[id] (edit and
// discard a draft) and POST /api/policies/[id]/[action] (submit, approve,
// publish). Each forwards to the matching /v1/policies upstream with the
// cookie bearer and passes the upstream status and body through; an
// unknown transition or a non-UUID id is refused here without reaching
// the platform.

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
import { POST as create } from "../../route";
import { DELETE as discard, PATCH as edit } from "../route";
import { POST as transition } from "./route";

const ID = "11111111-1111-1111-1111-111111112116";

function req(method: string, body?: unknown) {
  return new Request("http://x", {
    method,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
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

function bearerOf(init?: RequestInit) {
  return (init?.headers as Record<string, string>).Authorization;
}

describe("policy write BFF routes (#2116)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
  });

  test("create forwards the draft with the bearer", async () => {
    const calls = captureFetch(201, { policy: { id: ID } });
    const body = { title: "T", version: "1.0", predecessor_id: ID };
    const res = await create(req("POST", body));
    expect(res.status).toBe(201);
    expect(calls[0].url).toBe("http://atlas:8080/v1/policies");
    expect(calls[0].init?.method).toBe("POST");
    expect(calls[0].init?.body).toBe(JSON.stringify(body));
    expect(bearerOf(calls[0].init)).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });

  test("edit forwards PATCH and passes the platform's 409 through", async () => {
    const calls = captureFetch(409, {
      error: "only a draft policy can be edited",
    });
    const res = await edit(req("PATCH", { title: "T" }) as never, {
      params: Promise.resolve({ id: ID }),
    });
    expect(res.status).toBe(409);
    expect(calls[0].url).toBe(`http://atlas:8080/v1/policies/${ID}`);
    expect(calls[0].init?.method).toBe("PATCH");
  });

  test("discard forwards DELETE and passes 204 through", async () => {
    const calls = captureFetch(204, null);
    const res = await discard(req("DELETE") as never, {
      params: Promise.resolve({ id: ID }),
    });
    expect(res.status).toBe(204);
    expect(calls[0].url).toBe(`http://atlas:8080/v1/policies/${ID}`);
    expect(calls[0].init?.method).toBe("DELETE");
  });

  test("each transition goes to its platform route and method", async () => {
    for (const [action, method] of [
      ["submit", "PATCH"],
      ["approve", "PATCH"],
      ["publish", "POST"],
    ]) {
      const calls = captureFetch(200, { policy: { id: ID } });
      const body = action === "publish" ? { new_version: "1.0" } : undefined;
      const res = await transition(req("POST", body), {
        params: Promise.resolve({ id: ID, action }),
      });
      expect(res.status).toBe(200);
      expect(calls[0].url).toBe(
        `http://atlas:8080/v1/policies/${ID}/${action}`,
      );
      expect(calls[0].init?.method).toBe(method);
      expect(calls[0].init?.body).toBe(
        body === undefined ? undefined : JSON.stringify(body),
      );
      vi.restoreAllMocks();
    }
  });

  test("the approval rule's 403 reaches the page", async () => {
    captureFetch(403, { error: "policy: the submitter cannot approve" });
    const res = await transition(req("POST"), {
      params: Promise.resolve({ id: ID, action: "approve" }),
    });
    expect(res.status).toBe(403);
    expect(await res.json()).toEqual({
      error: "policy: the submitter cannot approve",
    });
  });

  test("an unknown transition or a bad id never reaches the platform", async () => {
    const calls = captureFetch(200, {});
    for (const [id, action] of [
      [ID, "supersede"],
      [ID, "toString"],
      ["not-a-uuid", "submit"],
    ]) {
      const res = await transition(req("POST"), {
        params: Promise.resolve({ id, action }),
      });
      expect([400, 404]).toContain(res.status);
    }
    const res = await edit(req("PATCH", {}) as never, {
      params: Promise.resolve({ id: "../x" }),
    });
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  test("no session is 401 without an upstream call", async () => {
    cookieStore.clear();
    const calls = captureFetch(200, {});
    const res = await create(req("POST", { title: "T" }));
    expect(res.status).toBe(401);
    expect(calls).toHaveLength(0);
  });
});
