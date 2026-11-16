// #2105 — vitest coverage for the risk treatment and acceptance BFF routes:
// PATCH /api/risks/[id]/treatment and /api/risks/[id]/acceptance/[action].
// Each forwards to the matching /v1/risks/{id} upstream with the cookie
// bearer and passes the upstream status and body through; an unknown
// acceptance action is refused here without reaching the platform.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../../../../../lib/test-utils/test-tokens";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { PATCH as treatment } from "../../treatment/route";
import { PATCH as acceptance } from "./route";

const RISK = "11111111-1111-1111-1111-111111111111";

function aparams(action: string, id = RISK) {
  return { params: Promise.resolve({ id, action }) };
}

function patchReq(body: unknown) {
  return new Request("http://x", {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

function captureFetch(status: number, body: unknown) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init });
      return new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    },
  );
  return calls;
}

describe("risk treatment + acceptance BFF routes (#2105)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
  });

  test("treatment forwards the body with the bearer", async () => {
    const calls = captureFetch(200, { risk: { id: RISK } });
    const res = await treatment(patchReq({ treatment: "avoid" }), {
      params: Promise.resolve({ id: RISK }),
    });
    expect(res.status).toBe(200);
    expect(calls[0].url).toBe(`http://atlas:8080/v1/risks/${RISK}/treatment`);
    expect(calls[0].init?.method).toBe("PATCH");
    expect(calls[0].init?.body).toBe(JSON.stringify({ treatment: "avoid" }));
    expect(
      (calls[0].init?.headers as Record<string, string>).Authorization,
    ).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });

  test("each acceptance action forwards to its platform route", async () => {
    for (const action of ["propose", "approve", "reject", "revoke"]) {
      const calls = captureFetch(200, { risk: { id: RISK } });
      const res = await acceptance(patchReq({ reason: "x" }), aparams(action));
      expect(res.status).toBe(200);
      expect(calls[0].url).toBe(
        `http://atlas:8080/v1/risks/${RISK}/acceptance/${action}`,
      );
      vi.restoreAllMocks();
    }
  });

  test("the approval rule's 403 passes through", async () => {
    const msg = "risk: the proposer cannot approve their own acceptance";
    captureFetch(403, { error: msg });
    const res = await acceptance(
      patchReq({ accepted_until: "2099-01-01" }),
      aparams("approve"),
    );
    expect(res.status).toBe(403);
    expect(((await res.json()) as { error: string }).error).toBe(msg);
  });

  test("an unknown action is 404 and never reaches the platform", async () => {
    const calls = captureFetch(200, {});
    const res = await acceptance(patchReq({}), aparams("expire"));
    expect(res.status).toBe(404);
    expect(calls).toHaveLength(0);
  });

  test("a non-UUID id is refused before the platform", async () => {
    const calls = captureFetch(200, {});
    const res = await acceptance(
      patchReq({}),
      aparams("approve", "not-a-uuid"),
    );
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });
});
