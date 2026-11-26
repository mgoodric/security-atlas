// #2434 — `/api/action-plans/[id]` refuses a non-UUID id before it reads
// the bearer or calls upstream. encodeURIComponent leaves `..` intact, so
// an unguarded `..` would collapse the upstream path to /v1/action-plans.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { DELETE, GET, PATCH } from "./route";

const PLAN = "00000000-0000-0000-0000-000024340001";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

function req(): never {
  return { text: () => Promise.resolve('{"title":"t"}') } as never;
}

const handlers = { GET, PATCH, DELETE } as const;

describe("/api/action-plans/[id]", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  for (const [method, handler] of Object.entries(handlers)) {
    for (const id of ["..", ".", "not-a-uuid"]) {
      const name = `${method} refuses id ${JSON.stringify(id)} without a fetch`;
      test(name, async () => {
        mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
        const fetchSpy = vi.spyOn(globalThis, "fetch");
        const res = await handler(req(), paramsFor(id));
        expect(res.status).toBe(400);
        expect(fetchSpy).not.toHaveBeenCalled();
        expect(mockCookieGet).not.toHaveBeenCalled();
      });
    }

    test(`${method} forwards a UUID id to /v1/action-plans/{id}`, async () => {
      mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
      const fetchSpy = vi
        .spyOn(globalThis, "fetch")
        .mockResolvedValueOnce(new Response("{}", { status: 200 }));
      const res = await handler(req(), paramsFor(PLAN));
      expect(res.status).toBe(200);
      expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
        `http://atlas:8080/v1/action-plans/${PLAN}`,
      );
      const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
      expect(init.method).toBe(method);
    });
  }
});
