// #1818 — vitest coverage for the organisation theme BFF route.
//
//   * PATCH forwards the body; DELETE forwards and passes a 204 through.
//   * a 409 (theme in use) passes through with its counts.
//   * a non-JSON PATCH body is a 400 here.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { DELETE, PATCH } from "./route";

const ctx = (ref: string) => ({ params: Promise.resolve({ ref }) });

describe("/api/themes/{ref}", () => {
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

  test("PATCH forwards the rename", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ theme: { name: "cloud-config" } }), {
        status: 200,
      }),
    );
    const res = await PATCH(
      new Request("http://localhost/api/themes/cloud-drift", {
        method: "PATCH",
        body: JSON.stringify({ name: "cloud-config" }),
      }),
      ctx("cloud-drift"),
    );
    expect(res.status).toBe(200);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/themes/cloud-drift");
    expect((init as RequestInit).method).toBe("PATCH");
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({
      name: "cloud-config",
    });
  });

  test("PATCH with a non-JSON body is a 400", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await PATCH(
      new Request("http://localhost/api/themes/x", {
        method: "PATCH",
        body: "nope",
      }),
      ctx("x"),
    );
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("DELETE passes a 204 through", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(null, { status: 204 }),
    );
    const res = await DELETE(
      new Request("http://localhost/api/themes/x"),
      ctx("x"),
    );
    expect(res.status).toBe(204);
  });

  test("DELETE of a theme in use passes the 409 and counts through", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "in use", risk_count: 2, rule_count: 1 }),
        {
          status: 409,
        },
      ),
    );
    const res = await DELETE(
      new Request("http://localhost/api/themes/x"),
      ctx("x"),
    );
    expect(res.status).toBe(409);
    expect(await res.json()).toMatchObject({ risk_count: 2, rule_count: 1 });
  });
});
