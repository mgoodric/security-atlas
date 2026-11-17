// #2117 — vitest coverage for the /audits/[id] BFFs: the single-period
// read, the freeze, and the per-control horizon read. Each forwards the
// bearer to its platform path and passes the upstream status through.
//
// Neutral test bearers only (P0-A5).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET as getPeriod } from "./route";
import { POST as freeze } from "./freeze/route";
import { GET as getControlState } from "./control-state/route";
import { GET as getOscalExport } from "./oscal-export/route";

const ID = "00000000-0000-0000-0000-000000002117";
const params = { params: Promise.resolve({ id: ID }) };
const dotdot = { params: Promise.resolve({ id: ".." }) };

function upstream(status: number, body: unknown) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function controlReq(control: string | null) {
  const url = new URL(`http://web/api/audits/${ID}/control-state`);
  if (control) url.searchParams.set("control", control);
  return { nextUrl: url } as unknown as Parameters<typeof getControlState>[0];
}

describe("/api/audits/[id] BFFs", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("GET 401 without the bearer cookie", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await getPeriod(new Request("http://web"), params);
    expect(res.status).toBe(401);
  });

  test("GET forwards to /v1/audit-periods/{id} and passes a 404 through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2117" });
    const spy = upstream(404, { error: "audit period not found" });
    const res = await getPeriod(new Request("http://web"), params);
    expect(res.status).toBe(404);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/audit-periods/${ID}`,
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer-2117",
    );
  });

  test("freeze POSTs to /v1/audit-periods/{id}/freeze and passes a 403 through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2117" });
    const spy = upstream(403, { error: "admin or grc_engineer role required" });
    const res = await freeze(new Request("http://web"), params);
    expect(res.status).toBe(403);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/audit-periods/${ID}/freeze`,
    );
    expect((spy.mock.calls[0]?.[1] as RequestInit).method).toBe("POST");
  });

  test("control-state 400 without a control, and no upstream call", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2117" });
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await getControlState(controlReq(null), params);
    expect(res.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("control-state forwards the control id", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2117" });
    const spy = upstream(200, { observations: [], count: 0 });
    const res = await getControlState(controlReq("ctl-1"), params);
    expect(res.status).toBe(200);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/audit-periods/${ID}/control-state?control=ctl-1`,
    );
  });

  // #2312: `..` survives encodeURIComponent, so without the guard
  // `/api/audits/%2E%2E/freeze` would reach upstream as `POST /v1/freeze`.
  test.each([
    ["GET", () => getPeriod(new Request("http://web"), dotdot)],
    ["freeze", () => freeze(new Request("http://web"), dotdot)],
    ["control-state", () => getControlState(controlReq("ctl-1"), dotdot)],
    ["oscal-export", () => getOscalExport(new Request("http://web"), dotdot)],
  ])(
    "%s refuses a non-UUID id with 400 and no upstream call",
    async (_n, call) => {
      mockCookieGet.mockReturnValue({ value: "test-bearer-2117" });
      const spy = vi.spyOn(globalThis, "fetch");
      const res = await call();
      expect(res.status).toBe(400);
      expect(spy).not.toHaveBeenCalled();
    },
  );

  test("the GET reads are marked no-store", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2117" });
    upstream(200, { id: ID, status: "open" });
    const period = await getPeriod(new Request("http://web"), params);
    expect(period.headers.get("Cache-Control")).toBe("no-store");
    upstream(200, { observations: [], count: 0 });
    const state = await getControlState(controlReq("ctl-1"), params);
    expect(state.headers.get("Cache-Control")).toBe("no-store");
  });
});
