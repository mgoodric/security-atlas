// #2434 — `POST /api/action-plans/[id]/verify` refuses a non-UUID id
// before it reads the bearer or calls upstream. An unguarded `..` would
// collapse `/v1/action-plans/../verify` to `/v1/verify`.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST } from "./route";

const PLAN = "00000000-0000-0000-0000-000024340002";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("POST /api/action-plans/[id]/verify", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  for (const id of ["..", ".", "not-a-uuid"]) {
    test(`refuses id ${JSON.stringify(id)} without a fetch`, async () => {
      mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
      const fetchSpy = vi.spyOn(globalThis, "fetch");
      const res = await POST({} as never, paramsFor(id));
      expect(res.status).toBe(400);
      expect(fetchSpy).not.toHaveBeenCalled();
      expect(mockCookieGet).not.toHaveBeenCalled();
    });
  }

  test("401 without a bearer", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await POST({} as never, paramsFor(PLAN));
    expect(res.status).toBe(401);
  });

  test("forwards a UUID id to /v1/action-plans/{id}/verify", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response("{}", { status: 403 }));
    const res = await POST({} as never, paramsFor(PLAN));
    expect(res.status).toBe(403);
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/action-plans/${PLAN}/verify`,
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_DEFAULT}`,
    );
  });
});
