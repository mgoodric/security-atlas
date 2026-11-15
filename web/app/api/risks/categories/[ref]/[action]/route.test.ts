// #1818 — vitest coverage for the category transition BFF route.
//
//   * archive / unarchive / set-default forward to upstream with the bearer.
//   * any other action is a 404 and never reaches upstream.
//   * upstream status and body pass through (a non-admin's 403).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST } from "./route";

function ctx(ref: string, action: string) {
  return { params: Promise.resolve({ ref, action }) };
}

const req = () =>
  new Request("http://localhost/api/risks/categories/x/y", { method: "POST" });

describe("POST /api/risks/categories/{ref}/{action}", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: "test-bearer-1818" });
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  for (const action of ["archive", "unarchive", "set-default"]) {
    test(`forwards ${action}`, async () => {
      const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response(JSON.stringify({ category: { slug: "privacy" } }), {
          status: 200,
        }),
      );
      const res = await POST(req(), ctx("privacy", action));
      expect(res.status).toBe(200);
      const [url, init] = fetchSpy.mock.calls[0];
      expect(String(url)).toBe(
        `http://atlas:8080/v1/risks/categories/privacy/${action}`,
      );
      expect((init as RequestInit).method).toBe("POST");
      expect((init as RequestInit).headers).toMatchObject({
        Authorization: "Bearer test-bearer-1818",
      });
    });
  }

  test("an unknown action is a 404 and never reaches upstream", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await POST(req(), ctx("privacy", "delete"));
    expect(res.status).toBe(404);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("upstream 403 passes through", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "role does not grant risk taxonomy management",
        }),
        {
          status: 403,
        },
      ),
    );
    const res = await POST(req(), ctx("privacy", "archive"));
    expect(res.status).toBe(403);
  });

  test("401 when the bearer cookie is missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await POST(req(), ctx("privacy", "archive"));
    expect(res.status).toBe(401);
  });
});
