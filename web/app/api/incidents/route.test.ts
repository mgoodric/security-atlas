import { beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("next/headers", () => ({
  cookies: vi.fn(),
}));

import { cookies } from "next/headers";

import { POST } from "./route";
import { GET } from "./route";

const mockedCookies = vi.mocked(cookies);

describe("GET /api/incidents", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("requires the signed-in bearer cookie", async () => {
    mockedCookies.mockResolvedValue({ get: () => undefined } as never);

    const res = await GET();

    expect(res.status).toBe(401);
    expect(await res.json()).toEqual({ error: "unauthenticated" });
  });

  test("forwards only cookie tenant context to upstream list", async () => {
    mockedCookies.mockResolvedValue({
      get: () => ({ value: "bearer-token" }),
    } as never);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ incidents: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();

    expect(res.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledWith("http://atlas:8080/v1/incidents", {
      headers: { Authorization: "Bearer bearer-token" },
      cache: "no-store",
    });
  });

  test("forwards create body to the upstream incident API", async () => {
    mockedCookies.mockResolvedValue({
      get: () => ({ value: "bearer-token" }),
    } as never);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ incident: { record: { id: "i-1" } } }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const body = {
      title: "Suspicious token use",
      severity: "p2",
      affected_systems: [{ name: "auth-prod" }],
      control_ids: ["33333333-3333-3333-3333-333333330001"],
    };

    const res = await POST(
      new Request("http://test/api/incidents", {
        method: "POST",
        body: JSON.stringify(body),
      }) as never,
    );

    expect(res.status).toBe(201);
    expect(fetchMock).toHaveBeenCalledWith("http://atlas:8080/v1/incidents", {
      method: "POST",
      headers: {
        Authorization: "Bearer bearer-token",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
      cache: "no-store",
    });
  });
});
