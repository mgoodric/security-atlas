// #1953 — vitest coverage for GET /api/admin/credentials.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * GET forwards ?minted_by to the upstream list, URL-encoded, so the
//     user revoke screen can list the live keys that user minted.
//   * GET without minted_by hits the bare upstream list.
//
// Test bearers use the neutral `test-bearer-1953` token (slice 069 P0-A9).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "@/lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();
vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "./route";

function makeGetReq(url = "http://test/api/admin/credentials") {
  const u = new URL(url);
  return { nextUrl: { searchParams: u.searchParams } } as never;
}

const MINTER = "11111111-1111-4111-8111-111111111111";

describe("GET /api/admin/credentials", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(makeGetReq());
    expect(res.status).toBe(401);
  });

  test("forwards minted_by to the upstream list", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-1953" });
    const item = { id: "key_a", minted_by: MINTER, last4: "abcd" };
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ items: [item] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await GET(
      makeGetReq(`http://test/api/admin/credentials?minted_by=${MINTER}`),
    );
    expect(res.status).toBe(200);
    const url = String(fetchSpy.mock.calls[0][0]);
    expect(url).toBe(
      `http://atlas:8080/v1/admin/credentials?minted_by=${MINTER}`,
    );
    const body = (await res.json()) as { items: unknown[] };
    expect(body.items).toEqual([item]);
  });

  test("without minted_by hits the bare list", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-1953" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await GET(makeGetReq());
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      "http://atlas:8080/v1/admin/credentials",
    );
  });
});
