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
    const res = await POST(
      {} as never,
      paramsFor("00000000-0000-0000-0000-000025580001"),
    );
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
    const res = await POST(
      {} as never,
      paramsFor("00000000-0000-0000-0000-000025580001"),
    );
    expect(res.status).toBe(200);
    expect(res.headers.get("Content-Type")).toBe("application/pdf");
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(
      "/v1/questionnaires/00000000-0000-0000-0000-000025580001/export-pdf",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
  });

  test("passes an upstream 409 chrome-absent refusal through (#2786)", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const refusal = {
      error:
        "PDF export is not enabled on this deployment: chrome is not installed",
      code: "feature_not_enabled",
    };
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(refusal), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST(
      {} as never,
      paramsFor("00000000-0000-0000-0000-000025580001"),
    );
    expect(res.status).toBe(409);
    expect(await res.json()).toEqual(refusal);
  });

  test("propagates upstream 503 render busy with its Retry-After", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "PDF export busy" }), {
        status: 503,
        headers: { "Content-Type": "application/json", "Retry-After": "15" },
      }),
    );
    const res = await POST(
      {} as never,
      paramsFor("00000000-0000-0000-0000-000025580001"),
    );
    expect(res.status).toBe(503);
    expect(res.headers.get("Retry-After")).toBe("15");
    // #2757: the 5xx body is masked on the wire.
    expect(await res.json()).toEqual({ error: "platform error" });
  });

  test("passes an upstream 4xx body through", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "questionnaire not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST(
      {} as never,
      paramsFor("00000000-0000-0000-0000-000025580001"),
    );
    expect(res.status).toBe(404);
    expect(await res.json()).toEqual({ error: "questionnaire not found" });
  });
});
