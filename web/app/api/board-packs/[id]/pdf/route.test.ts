// #2786 — vitest coverage for GET /api/board-packs/[id]/pdf error passthrough.

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

import { GET } from "./route";

const PACK_ID = "00000000-0000-0000-0000-000027860001";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/board-packs/[id]/pdf", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("passes an upstream 409 chrome-absent refusal through", async () => {
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
    const res = await GET({} as Request, paramsFor(PACK_ID));
    expect(res.status).toBe(409);
    expect(res.headers.get("Retry-After")).toBeNull();
    expect(await res.json()).toEqual(refusal);
  });

  test("propagates upstream 503 render busy with its Retry-After", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "PDF rendering busy" }), {
        status: 503,
        headers: { "Content-Type": "application/json", "Retry-After": "15" },
      }),
    );
    const res = await GET({} as Request, paramsFor(PACK_ID));
    expect(res.status).toBe(503);
    expect(res.headers.get("Retry-After")).toBe("15");
    // #2757: the 5xx body is masked on the wire.
    expect(await res.json()).toEqual({ error: "platform error" });
  });
});
