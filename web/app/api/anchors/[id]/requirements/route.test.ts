// Slice 484 — vitest coverage for the version-aware reverse-traversal proxy
// web/app/api/anchors/[id]/requirements/route.ts (AC-6).
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` upstream.
//   * Absent ?framework_version, hits the bare upstream path (no pin → the
//     upstream defaults to each framework's current version, ADR 0019 §4).
//   * With a valid ?framework_version=slug:version, forwards it upstream
//     verbatim (URL-encoded).
//   * Drops a malformed framework_version (never forwards arbitrary client
//     text into the upstream query string).
//   * Propagates the upstream error status.
//
// Neutral `test-bearer-484` token — NO vendor token prefixes (slice 098 P0-A5).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

const ANCHOR_DETAIL = {
  anchor: {
    id: "00000000-0000-0000-0000-0000000000a1",
    scf_id: "IAC-06",
    family: "IAC",
    name: "Multi-Factor Authentication",
    description: "",
  },
  requirements: [],
};

function reqFor(query: string) {
  return {
    nextUrl: new URL(
      `http://localhost/api/anchors/IAC-06/requirements${query}`,
    ),
  };
}

function ctxFor(id: string) {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/anchors/[id]/requirements", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(reqFor("") as never, ctxFor("IAC-06") as never);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("no pin → bare upstream path, bearer forwarded", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-484" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(ANCHOR_DETAIL), { status: 200 }),
      );

    const res = await GET(reqFor("") as never, ctxFor("IAC-06") as never);
    expect(res.status).toBe(200);

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/anchors/IAC-06/requirements");
    expect(calledURL).not.toContain("framework_version");

    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const auth = new Headers(init?.headers).get("Authorization");
    expect(auth).toBe("Bearer test-bearer-484");
  });

  test("valid framework_version pin is forwarded upstream", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-484" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(ANCHOR_DETAIL), { status: 200 }),
      );

    const res = await GET(
      reqFor("?framework_version=soc2:2017") as never,
      ctxFor("IAC-06") as never,
    );
    expect(res.status).toBe(200);

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("framework_version=soc2%3A2017");
  });

  test("malformed framework_version is dropped (not forwarded)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-484" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(ANCHOR_DETAIL), { status: 200 }),
      );

    const res = await GET(
      reqFor("?framework_version=not-a-valid-pin%20OR%201=1") as never,
      ctxFor("IAC-06") as never,
    );
    expect(res.status).toBe(200);

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).not.toContain("framework_version");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-484" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "anchor not found" }), {
        status: 404,
      }),
    );

    const res = await GET(reqFor("") as never, ctxFor("NOPE") as never);
    expect(res.status).toBe(404);
  });
});
