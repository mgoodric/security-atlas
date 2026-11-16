// #1778 — bulk-approve is one POST to the platform's bulk endpoint, with
// the caller's own bearer; the platform's status passes through.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST } from "./route";

describe("POST /api/controls/remap-proposals/approve-unchanged", () => {
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

  test("one POST to the bulk endpoint", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ approved_count: 2, approved: [], skipped: [] }),
          { status: 200 },
        ),
      );
    const res = await POST();
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
      "http://atlas:8080/v1/controls/remap-proposals/approve-unchanged",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer reviewer-bearer",
    );
  });

  test("a machine credential's 403 passes through", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "a person must decide" }), {
        status: 403,
      }),
    );
    const res = await POST();
    expect(res.status).toBe(403);
  });
});
