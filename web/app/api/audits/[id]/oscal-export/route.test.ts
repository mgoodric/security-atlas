// Slice 457 — vitest coverage for the OSCAL signed-export download BFF
// (web/app/api/audits/[id]/oscal-export/route.ts).
//
// The route guarantees:
//   * 401 when the bearer cookie is missing (no upstream call).
//   * GET-here maps to POST-upstream against the slice-457 :download verb,
//     forwarding the bearer.
//   * On success the upstream bytes + Content-Disposition + Content-Type
//     pass through verbatim, with X-Content-Type-Options: nosniff added.
//   * On upstream error (404 cross-tenant/unknown, 409 not-frozen) the
//     status + body propagate and NO attachment disposition is set.
//   * The bearer is never echoed into the response.
//
// Mirrors the slice 043 board-pack PDF BFF test shape + the slice 102/149
// audits BFF test. All test bearers use the neutral `test-bearer-457`
// token — NO vendor token prefixes (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`).

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

const PERIOD_ID = "00000000-0000-0000-0000-0000000457aa";
const FILENAME = `oscal-bundle-${PERIOD_ID}-2026-03-31.json`;

// A minimal signed envelope — the four members + the slice-413 signature
// manifest. The BFF streams these bytes verbatim; the test asserts the
// passthrough is byte-faithful and the headers are set.
function envelope(): string {
  return JSON.stringify({
    audit_period_id: PERIOD_ID,
    frozen_at: "2026-03-31T00:00:00Z",
    oscal_version: "1.1.2",
    signature: {
      mode: "embedded-ed25519",
      algorithm: "ed25519",
      digest: "00".repeat(32),
      signature: "11".repeat(64),
    },
    members: [
      { model_type: "system-security-plan", filename: "ssp.json" },
      { model_type: "assessment-plan", filename: "ap.json" },
      { model_type: "assessment-results", filename: "ar.json" },
      { model_type: "plan-of-action-and-milestones", filename: "poam.json" },
    ],
  });
}

function ctxFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/audits/[id]/oscal-export (slice 457 download BFF)", () => {
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

  test("401 when bearer cookie missing (no upstream call)", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET(new Request("http://x"), ctxFor(PERIOD_ID));
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("GET maps to POST :download upstream and streams the attachment back", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-457" });

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(envelope(), {
            status: 200,
            headers: {
              "Content-Type": "application/json",
              "Content-Disposition": `attachment; filename="${FILENAME}"`,
              "X-Content-Type-Options": "nosniff",
            },
          });
        },
      );

    const res = await GET(new Request("http://x"), ctxFor(PERIOD_ID));
    expect(res.status).toBe(200);

    // GET-here -> POST-upstream against the :download verb.
    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(capturedURL).toBe(
      `http://atlas:8080/v1/audit-periods/${PERIOD_ID}/oscal-export:download`,
    );
    expect(capturedInit?.method).toBe("POST");
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-bearer-457");

    // The attachment disposition + content-type + nosniff ride to the
    // browser so a `download` event fires with the platform's filename.
    expect(res.headers.get("Content-Disposition")).toBe(
      `attachment; filename="${FILENAME}"`,
    );
    expect(res.headers.get("Content-Type")).toBe("application/json");
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");

    // Body is the verbatim signed envelope (the signing manifest rides
    // in the downloaded bundle — AC-4).
    const text = await res.text();
    const parsed = JSON.parse(text) as {
      audit_period_id: string;
      signature: { algorithm: string };
      members: unknown[];
    };
    expect(parsed.audit_period_id).toBe(PERIOD_ID);
    expect(parsed.signature.algorithm).toBe("ed25519");
    expect(parsed.members).toHaveLength(4);

    // The bearer must never be echoed into the response.
    expect(text).not.toContain("test-bearer-457");
  });

  test("upstream 404 (cross-tenant/unknown period) propagates, no attachment", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-457" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "audit period not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(new Request("http://x"), ctxFor(PERIOD_ID));
    expect(res.status).toBe(404);
    expect(res.headers.get("Content-Disposition")).toBeNull();
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("audit period not found");
  });

  test("upstream 409 (not-frozen) propagates verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-457" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "audit period is not frozen" }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(new Request("http://x"), ctxFor(PERIOD_ID));
    expect(res.status).toBe(409);
    expect(res.headers.get("Content-Disposition")).toBeNull();
  });

  test("falls back to a default attachment filename when upstream omits one", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-457" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(envelope(), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(new Request("http://x"), ctxFor(PERIOD_ID));
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Disposition")).toBe(
      `attachment; filename="oscal-bundle-${PERIOD_ID}.json"`,
    );
  });
});
