// #2392 — vitest coverage for the anchor-history BFF proxy. Same shape as
// the history proxy's test: 401 without the cookie, the bearer forwarded,
// and the platform's 404 (another tenant's control) passed through.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { GET } from "./route";

const CONTROL_ID = "33333333-3333-3333-3333-333333332392";

function call(id = CONTROL_ID) {
  const req = new Request(`http://localhost/api/controls/${id}/anchor-history`);
  return GET(req as unknown as Parameters<typeof GET>[0], {
    params: Promise.resolve({ id }),
  });
}

describe("GET /api/controls/[id]/anchor-history", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when atlas_jwt cookie is absent", async () => {
    const res = await call();
    expect(res.status).toBe(401);
  });

  test("rejects a non-UUID id before calling the platform", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-2392");
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await call("not-a-uuid");
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards bearer to upstream and passes 200 through", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-2392");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          control_id: CONTROL_ID,
          decisions: [
            {
              id: "44444444-4444-4444-4444-444444444444",
              decision: "approved",
              bulk: false,
              legacy_scf_id: "IAC-01",
              legacy_release: "2024.1",
              new_scf_id: "IAC-01.1",
              new_release: "2025.1",
              decided_by: "system:scf-import",
              decided_at: "2026-11-01T12:00:00Z",
            },
          ],
          count: 1,
          next_cursor: "",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await call();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { decisions: unknown[]; count: number };
    expect(body.count).toBe(1);
    expect(body.decisions).toHaveLength(1);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(`/v1/controls/${CONTROL_ID}/anchor-history`);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-2392");
  });

  test("passes the platform's 404 through for another tenant's control", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-2392");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "control not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await call();
    expect(res.status).toBe(404);
  });
});
