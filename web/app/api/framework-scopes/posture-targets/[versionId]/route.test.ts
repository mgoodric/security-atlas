// #2136 — vitest coverage for the posture-target BFF: GET and PUT forward
// the bearer to the platform path, refuse a non-UUID id before any upstream
// call, forward only the four target fields, and pass the upstream status
// through (the platform owns the 403 / 400 / 404 / 409 refusals).
//
// Neutral test bearers only (P0-A5).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET, PUT } from "./route";

const FV = "00000000-0000-0000-0000-000000002136";
const params = { params: Promise.resolve({ versionId: FV }) };
const TARGET = {
  target_value: 90,
  warning_threshold: 80,
  critical_threshold: 60,
  direction: "higher_is_better",
};

function upstream(status: number, body: unknown) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function put(body: string) {
  return new Request("http://web", { method: "PUT", body });
}

describe("/api/framework-scopes/posture-targets/[versionId]", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: "test-bearer-2136" });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("GET forwards to the platform with the bearer", async () => {
    const spy = upstream(200, { framework_version_id: FV, ...TARGET });
    const res = await GET(new Request("http://web"), params);
    expect(res.status).toBe(200);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/framework-scopes/posture-targets/${FV}`,
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer-2136",
    );
  });

  test("GET passes the platform's 404 (no target) through", async () => {
    upstream(404, {
      error: "no posture target set for this framework version",
    });
    expect((await GET(new Request("http://web"), params)).status).toBe(404);
  });

  test("PUT forwards only the four target fields", async () => {
    const spy = upstream(200, { framework_version_id: FV, ...TARGET });
    const res = await PUT(
      put(JSON.stringify({ ...TARGET, tenant_id: "x", updated_by: "y" })),
      params,
    );
    expect(res.status).toBe(200);
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual(TARGET);
  });

  test("PUT passes the platform's 403 for a viewer through", async () => {
    upstream(403, {
      error: "admin or grc_engineer role required to set a posture target",
    });
    expect((await PUT(put(JSON.stringify(TARGET)), params)).status).toBe(403);
  });

  test("PUT refuses a non-object body before any upstream call", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    expect((await PUT(put("{"), params)).status).toBe(400);
    expect((await PUT(put("[1]"), params)).status).toBe(400);
    expect((await PUT(put("null"), params)).status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("a non-UUID version id is refused before any upstream call", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const bad = { params: Promise.resolve({ versionId: ".." }) };
    expect((await GET(new Request("http://web"), bad)).status).toBe(400);
    expect((await PUT(put(JSON.stringify(TARGET)), bad)).status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });
});
