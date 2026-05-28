// Slice 123 — vitest seed coverage for app/api/install-state/route.ts.
//
// The route guarantees:
//   * Upstream URL is `${apiBaseURL()}/v1/install-state` (the public
//     bearer-exempt endpoint surfaced by slice 073).
//   * No bearer cookie is read or forwarded (the upstream is public,
//     same contract as the slice-072 /api/version BFF).
//   * Upstream 2xx JSON body passes through verbatim.
//   * Upstream non-2xx (5xx, 404, etc.) maps to {first_install: false}
//     with status 200 — preserving the slice-073 P0-A5 anti-criterion
//     (a metadata failure must NEVER block the production sign-in path).
//   * Network failure also maps to {first_install: false} with status 200.
//
// Hard rule (P0-A9, slice 069 lesson): test fixtures use neutral strings
// only — NO vendor token prefixes (ghp_*, sk_*, AKIA*, eyJ*).

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

import { GET } from "./route";

describe("GET /api/install-state", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("proxies upstream /v1/install-state verbatim on first_install=true", async () => {
    const upstreamBody = JSON.stringify({ first_install: true });

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(upstreamBody, {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        },
      );

    const res = await GET();

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(capturedURL).toBe("http://atlas:8080/v1/install-state");
    // Public endpoint — bearer must NOT be forwarded.
    const headers = (capturedInit?.headers ?? {}) as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { first_install?: boolean };
    expect(body.first_install).toBe(true);
  });

  test("proxies upstream first_install=false verbatim", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ first_install: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { first_install?: boolean };
    expect(body.first_install).toBe(false);
  });

  test("maps upstream 5xx to {first_install: false} status 200 (P0-A5)", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("", { status: 503 }),
    );

    const res = await GET();
    // The login form MUST render even when the metadata endpoint is
    // broken — so the BFF returns 200 with a safe default rather than
    // surfacing the upstream failure.
    expect(res.status).toBe(200);
    const body = (await res.json()) as { first_install?: boolean };
    expect(body.first_install).toBe(false);
  });

  test("maps upstream 404 to {first_install: false} status 200", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("not found", { status: 404 }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { first_install?: boolean };
    expect(body.first_install).toBe(false);
  });

  test("network error -> {first_install: false} status 200", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("ECONNREFUSED"));

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { first_install?: boolean };
    expect(body.first_install).toBe(false);
  });

  test("sets Cache-Control: no-store on success", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ first_install: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("forwards no Authorization header to upstream", async () => {
    // Belt-and-suspenders for the public-endpoint contract: even if the
    // user is signed in and a session cookie is present, the bearer
    // must NOT be sent upstream. The endpoint is intentionally public
    // (slice 073 bearer-exempt list in internal/api/httpserver.go).
    let capturedInit: RequestInit | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        capturedInit = init;
        return new Response(JSON.stringify({ first_install: false }), {
          status: 200,
        });
      },
    );

    await GET();
    const headers = (capturedInit?.headers ?? {}) as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
    expect(headers.authorization).toBeUndefined();
  });
});
