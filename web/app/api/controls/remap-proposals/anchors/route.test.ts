// #1778 — the anchor lookup / search BFF reads the global SCF catalog
// with the caller's bearer, and validates codes and release labels before
// they reach an upstream path.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "./route";

const get = (qs: string) =>
  GET(new Request(`http://web/api/controls/remap-proposals/anchors?${qs}`));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });

describe("GET /api/controls/remap-proposals/anchors", () => {
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

  test.each([
    ["release=..&codes=IAC-01"],
    ["release=B&codes=.."],
    ["release=B&codes=a%2Fb"],
    ["release=B&q=x"],
  ])("refuses %s before any upstream call", async (qs) => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await get(qs);
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("lookup maps a code the release lacks to null", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (url) =>
        String(url).includes("/v1/anchors/IAC-10")
          ? json({
              anchor: { scf_id: "IAC-10", name: "Authenticator Management" },
            })
          : json({ error: "not found" }, 404),
      );
    const res = await get("release=B&codes=IAC-10,IAC-99");
    expect(res.status).toBe(200);
    const body = (await res.json()) as { anchors: Record<string, unknown> };
    expect(body.anchors["IAC-10"]).toMatchObject({ scf_id: "IAC-10" });
    expect(body.anchors["IAC-99"]).toBeNull();
    const urls = fetchSpy.mock.calls.map((c) => String(c[0]));
    expect(urls).toContain("http://atlas:8080/v1/anchors/IAC-10?scf_release=B");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer reviewer-bearer",
    );
  });

  test("search filters the release's anchors by code or title", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
      const u = String(url);
      if (u.endsWith("/v1/frameworks/scf/versions")) {
        return json({ versions: [{ id: "v-b", version: "B" }] });
      }
      return json({
        anchors: [
          { scf_id: "IAC-10", name: "Authenticator Management" },
          { scf_id: "NET-01", name: "Network Security" },
        ],
      });
    });
    const res = await get("release=B&q=authent");
    const body = (await res.json()) as { anchors: { scf_id: string }[] };
    expect(body.anchors.map((a) => a.scf_id)).toEqual(["IAC-10"]);
  });

  test("search on an unknown release is a 404", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ versions: [] }));
    const res = await get("release=Z&q=iac");
    expect(res.status).toBe(404);
  });
});
