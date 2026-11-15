// Issue #2176 — vitest coverage for web/app/api/controls/needs-reanchoring.
//
// The route: 401 without the bearer cookie; forwards the bearer to
// GET /v1/controls/needs-reanchoring; passes the envelope through; passes
// an upstream error status (e.g. 403 for a role that cannot read controls)
// through as its own status, so the banner hides.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

describe("GET /api/controls/needs-reanchoring", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET();
    expect(res.status).toBe(401);
  });

  test("forwards the bearer and passes the envelope through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2176" });
    const envelope = {
      controls: [
        {
          id: "c1",
          bundle_id: "tenant-iac-21",
          title: "PAM",
          legacy_scf_id: "IAC-21",
          legacy_anchor_title: "Privileged Account Management",
          legacy_release: "test-2026.1",
          current_release: "test-2026.3",
          note: "IAC-21 -> IAC-10 in test-2026.3 (concept)",
        },
      ],
      count: 1,
    };
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(envelope), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(envelope);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe("http://atlas:8080/v1/controls/needs-reanchoring");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-2176");
  });

  test("propagates an upstream 403", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2176" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("forbidden", { status: 403 }),
    );
    const res = await GET();
    expect(res.status).toBe(403);
  });
});
