import { beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("next/headers", () => ({
  cookies: vi.fn(),
}));

import { cookies } from "next/headers";

import { POST } from "./route";

const mockedCookies = vi.mocked(cookies);

describe("POST /api/incidents/:id/close", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("requires the signed-in bearer cookie", async () => {
    mockedCookies.mockResolvedValue({ get: () => undefined } as never);

    const res = await POST(new Request("http://test") as never, {
      params: Promise.resolve({ id: "63363363-6336-6336-6336-633633633001" }),
    });

    expect(res.status).toBe(401);
    expect(await res.json()).toEqual({ error: "unauthenticated" });
  });

  test("forwards postmortem closure to the upstream close path", async () => {
    mockedCookies.mockResolvedValue({
      get: () => ({ value: "bearer-token" }),
    } as never);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          incident: {
            record: {
              id: "63363363-6336-6336-6336-633633633001",
              status: "closed",
              postmortem_summary: "Root cause and follow-up captured.",
            },
          },
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );
    const body = {
      postmortem_summary: "Root cause and follow-up captured.",
      observed_at: "2026-08-04T12:00:00.000Z",
    };

    const res = await POST(
      new Request(
        "http://test/api/incidents/63363363-6336-6336-6336-633633633001/close",
        {
          method: "POST",
          body: JSON.stringify(body),
        },
      ) as never,
      {
        params: Promise.resolve({
          id: "63363363-6336-6336-6336-633633633001",
        }),
      },
    );

    expect(res.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledWith(
      "http://atlas:8080/v1/incidents/63363363-6336-6336-6336-633633633001/close",
      {
        method: "POST",
        headers: {
          Authorization: "Bearer bearer-token",
          "Content-Type": "application/json",
        },
        body: JSON.stringify(body),
        cache: "no-store",
      },
    );
  });
});
