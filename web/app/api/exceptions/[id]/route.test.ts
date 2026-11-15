// #2113 — vitest coverage for the /exceptions BFFs added for the request
// form and the detail page: create, the single read, the audit log and
// the lifecycle transitions. Each forwards the bearer to its platform
// path and passes the upstream status through.
//
// Neutral test bearers only (P0-A5).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST as create } from "../route";
import { GET as getOne } from "./route";
import { GET as getAuditLog } from "./audit-log/route";
import { PATCH as transition } from "./[transition]/route";

const ID = "00000000-0000-0000-0000-000000002113";
const params = { params: Promise.resolve({ id: ID }) };
const tparams = (t: string) => ({
  params: Promise.resolve({ id: ID, transition: t }),
});

function upstream(status: number, body: unknown) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function jsonReq(body: unknown, method = "POST") {
  return new Request("http://web", {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

describe("/api/exceptions BFFs (#2113)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("GET one 401 without the bearer cookie", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await getOne(new Request("http://web"), params);
    expect(res.status).toBe(401);
  });

  test("GET one forwards to /v1/exceptions/{id} and passes a 404 through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
    const spy = upstream(404, { error: "exception not found" });
    const res = await getOne(new Request("http://web"), params);
    expect(res.status).toBe(404);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/exceptions/${ID}`,
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer-2113",
    );
  });

  test("audit log forwards to /v1/exceptions/{id}/audit-log", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
    const spy = upstream(200, { entries: [], count: 0 });
    const res = await getAuditLog(new Request("http://web"), params);
    expect(res.status).toBe(200);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/exceptions/${ID}/audit-log`,
    );
  });

  test("create POSTs the body to /v1/exceptions and passes a 400 through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
    const spy = upstream(400, {
      error: "scope_cell_predicate must not be null",
    });
    const body = { control_id: ID, scope_cell_predicate: { op: "true" } };
    const res = await create(jsonReq(body));
    expect(res.status).toBe(400);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      "http://atlas:8080/v1/exceptions",
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual(body);
  });

  test("create 400 on a non-JSON body, and no upstream call", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await create(
      new Request("http://web", { method: "POST", body: "not json" }),
    );
    expect(res.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test.each(["approve", "deny", "activate", "revoke"])(
    "%s PATCHes /v1/exceptions/{id}/%s with the body",
    async (t) => {
      mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
      const spy = upstream(200, { exception: { id: ID } });
      const res = await transition(
        jsonReq({ reason: "fixed early" }, "PATCH"),
        tparams(t),
      );
      expect(res.status).toBe(200);
      expect(String(spy.mock.calls[0]?.[0])).toBe(
        `http://atlas:8080/v1/exceptions/${ID}/${t}`,
      );
      const init = spy.mock.calls[0]?.[1] as RequestInit;
      expect(init.method).toBe("PATCH");
      expect(JSON.parse(String(init.body))).toEqual({ reason: "fixed early" });
    },
  );

  test("a revoke the platform refuses passes its 403 through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
    upstream(403, { error: "approver role required" });
    const res = await transition(
      jsonReq({ reason: "x" }, "PATCH"),
      tparams("revoke"),
    );
    expect(res.status).toBe(403);
  });

  test("an unknown transition is 404 and never reaches the platform", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2113" });
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await transition(jsonReq({}, "PATCH"), tparams("expire"));
    expect(res.status).toBe(404);
    expect(spy).not.toHaveBeenCalled();
  });
});
