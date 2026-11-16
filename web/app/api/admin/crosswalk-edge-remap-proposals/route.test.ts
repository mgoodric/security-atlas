// #1778 — the edge remap BFF forwards the caller's own bearer (never a
// service credential) and passes the platform's 403 for a tenant admin
// through, so a non-super-admin gets no edge proposals from this route.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "./route";

describe("GET /api/admin/crosswalk-edge-remap-proposals", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 without the bearer cookie, and no upstream call", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("a tenant admin gets the platform's 403, sent with their own bearer", async () => {
    mockCookieGet.mockReturnValue({ value: "tenant-admin-bearer" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "super_admin required" }), {
        status: 403,
      }),
    );
    const res = await GET();
    expect(res.status).toBe(403);
    const body = (await res.json()) as Record<string, unknown>;
    expect(body.proposals).toBeUndefined();
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
      "http://atlas:8080/v1/admin/crosswalk-edge-remap-proposals?status=open",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer tenant-admin-bearer");
  });

  test("a super-admin gets the proposals through", async () => {
    mockCookieGet.mockReturnValue({ value: "super-bearer" });
    const envelope = { proposals: [{ id: "e1" }] };
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(envelope), { status: 200 }),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(envelope);
  });
});
