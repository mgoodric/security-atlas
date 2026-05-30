// Slice 263 — vitest coverage for POST /api/questionnaires/[id]/export-pdf.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { POST } from "./route";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("POST /api/questionnaires/[id]/export-pdf", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await POST({} as never, paramsFor("q1"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer + preserves application/pdf content-type", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const pdfBytes = new Uint8Array([0x25, 0x50, 0x44, 0x46]); // "%PDF"
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(pdfBytes.buffer, {
        status: 200,
        headers: { "Content-Type": "application/pdf" },
      }),
    );
    const res = await POST({} as never, paramsFor("q1"));
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("application/pdf");
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/q1/export-pdf");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
  });

  test("propagates upstream 503 chrome unavailable", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "PDF export disabled" }), {
        status: 503,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST({} as never, paramsFor("q1"));
    expect(res.status).toBe(503);
  });
});
