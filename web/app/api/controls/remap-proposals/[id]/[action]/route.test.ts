// #1778 — the remap decision BFF: the id must be a UUID, the action one of
// approve / reject / keep-legacy, and only scf_id (approve) and reason
// reach the platform, with the caller's own bearer.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST } from "./route";

const ID = "00000000-0000-4000-8000-000000001778";

function call(id: string, action: string, body?: unknown) {
  const req = new Request(
    `http://web/api/controls/remap-proposals/${id}/${action}`,
    {
      method: "POST",
      body: body === undefined ? undefined : JSON.stringify(body),
    },
  );
  return POST(req, { params: Promise.resolve({ id, action }) });
}

describe("POST /api/controls/remap-proposals/[id]/[action]", () => {
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

  test("a non-UUID id is refused before any upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await call("..", "approve");
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("an unknown action is refused before any upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await call(ID, "delete");
    expect(res.status).toBe(404);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("approve forwards scf_id and reason only", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ decision: "approved" }), { status: 200 }),
      );
    const res = await call(ID, "approve", {
      scf_id: "IAC-10",
      reason: "split: privileged half",
      bulk: true,
    });
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/controls/remap-proposals/${ID}/approve`,
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({
      scf_id: "IAC-10",
      reason: "split: privileged half",
    });
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer reviewer-bearer",
    );
  });

  test("keep-legacy drops scf_id and passes an upstream 403 through", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: "forbidden" }), { status: 403 }),
      );
    const res = await call(ID, "keep-legacy", { scf_id: "IAC-10" });
    expect(res.status).toBe(403);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({});
  });
});
