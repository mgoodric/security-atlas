// #2118 — vitest coverage for the period-auditor BFFs: list, assign and
// remove. Each forwards the bearer to its platform path, refuses a non-UUID
// id before any upstream call, and passes the upstream status through.
//
// Neutral test bearers only (P0-A5).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET, POST } from "./route";
import { DELETE } from "./[userId]/route";

const ID = "00000000-0000-0000-0000-000000002118";
const USER = "00000000-0000-0000-0000-0000000021a8";
const params = { params: Promise.resolve({ id: ID }) };

function upstream(status: number, body: unknown) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(status === 204 ? null : JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function post(body: string) {
  return new Request("http://web", { method: "POST", body });
}

describe("/api/audits/[id]/auditors BFFs", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: "test-bearer-2118" });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("GET forwards to /v1/audit-periods/{id}/auditors with the bearer", async () => {
    const spy = upstream(200, { auditors: [], candidates: [], count: 0 });
    const res = await GET(new Request("http://web"), params);
    expect(res.status).toBe(200);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/audit-periods/${ID}/auditors`,
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer-2118",
    );
  });

  test("GET passes the platform's 403 through", async () => {
    upstream(403, { error: "admin role required" });
    const res = await GET(new Request("http://web"), params);
    expect(res.status).toBe(403);
  });

  test("a non-UUID period id is refused before any upstream call", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const bad = { params: Promise.resolve({ id: ".." }) };
    expect((await GET(new Request("http://web"), bad)).status).toBe(400);
    expect(
      (await POST(post(JSON.stringify({ user_id: USER })), bad)).status,
    ).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("POST forwards only the user_id to the platform", async () => {
    const spy = upstream(201, { assigned: true });
    const res = await POST(
      post(JSON.stringify({ user_id: USER, tenant_id: "x" })),
      params,
    );
    expect(res.status).toBe(201);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/audit-periods/${ID}/auditors`,
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ user_id: USER });
  });

  test("POST refuses a body without a UUID user_id", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    expect((await POST(post("{"), params)).status).toBe(400);
    expect(
      (await POST(post(JSON.stringify({ user_id: "user:x" })), params)).status,
    ).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("POST passes the platform's 400 for a non-auditor through", async () => {
    upstream(400, { error: "user_id is not an active auditor in this tenant" });
    const res = await POST(post(JSON.stringify({ user_id: USER })), params);
    expect(res.status).toBe(400);
  });

  test("DELETE forwards to /v1/audit-periods/{id}/auditors/{userId}", async () => {
    const spy = upstream(204, null);
    const res = await DELETE(new Request("http://web"), {
      params: Promise.resolve({ id: ID, userId: USER }),
    });
    expect(res.status).toBe(204);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/audit-periods/${ID}/auditors/${USER}`,
    );
    expect((spy.mock.calls[0]?.[1] as RequestInit).method).toBe("DELETE");
  });

  test("DELETE refuses a non-UUID user id before any upstream call", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await DELETE(new Request("http://web"), {
      params: Promise.resolve({ id: ID, userId: ".." }),
    });
    expect(res.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });
});
