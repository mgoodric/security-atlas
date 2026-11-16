// #1778 — the remap proposal list BFF forwards only the class and status
// filters, with the caller's own bearer.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "./route";

const empty = () =>
  new Response(JSON.stringify({ proposals: [], count: 0 }), { status: 200 });

describe("GET /api/controls/remap-proposals", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: "reviewer-bearer" });
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("forwards class and status, drops anything else", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(empty());
    const res = await GET(
      new Request(
        "http://web/api/controls/remap-proposals?class=split&status=open&tenant_id=x",
      ),
    );
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
      "http://atlas:8080/v1/controls/remap-proposals?class=split&status=open",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer reviewer-bearer",
    );
  });

  test("no filters: the bare path", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(empty());
    await GET(new Request("http://web/api/controls/remap-proposals"));
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
      "http://atlas:8080/v1/controls/remap-proposals",
    );
  });
});
