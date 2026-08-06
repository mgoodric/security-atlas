// Slice 536b-1 — vitest coverage for the crosswalk-edge content-edit BFF route.
//
// The route is a thin admin proxy: it must (1) 401 without the bearer cookie
// before any upstream call, (2) forward the JSON patch body verbatim to
// PATCH /v1/admin/crosswalk-edges/{id} (the upstream owns validation, the
// admin gate, the D-536b-1 tier gate, and the same-transaction audit row),
// (3) pass the upstream status + body through verbatim — including the 409
// tier-gate refusal and a non-admin 403 — and (4) mark the mutable resource
// no-store.

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../../../../lib/test-utils/test-tokens";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { PATCH } from "./route";

function makeReq(body: unknown): { json: () => Promise<unknown> } {
  return { json: async () => body };
}

function badJSONReq(): { json: () => Promise<unknown> } {
  return {
    json: async () => {
      throw new SyntaxError("bad json");
    },
  };
}

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("PATCH /api/admin/crosswalk-edges/[id] BFF", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("401 without the bearer cookie, before any upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await PATCH(
      makeReq({ strength: 0.5 }) as never,
      paramsFor("e1"),
    );
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards the patch body verbatim and passes the edit response through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const upstream = {
      edge_id: "0d4e77aa-0000-0000-0000-00000000000e",
      from: { relationship_type: "equal", strength: 1, rationale: "old" },
      to: { relationship_type: "subset_of", strength: 0.7, rationale: "new" },
      editor_id: "0d4e77aa-0000-0000-0000-00000000000a",
      note: "downgraded after review",
      mapping_tier: "under_review",
      created_at: "2026-08-04T00:00:00Z",
    };
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(upstream), { status: 200 }),
      );

    const res = await PATCH(
      makeReq({
        relationship_type: "subset_of",
        strength: 0.7,
        rationale: "new",
        note: "downgraded after review",
      }) as never,
      paramsFor("0d4e77aa-0000-0000-0000-00000000000e"),
    );

    expect(res.status).toBe(200);
    const url = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(url).toContain(
      "/v1/admin/crosswalk-edges/0d4e77aa-0000-0000-0000-00000000000e",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("PATCH");
    // Bearer forwarded as Authorization.
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_TOKEN}`,
    );
    // Patch body rides through verbatim — the upstream is the single
    // validator; the BFF adds and strips nothing.
    const sent = String(init.body ?? "");
    expect(sent).toContain('"relationship_type":"subset_of"');
    expect(sent).toContain('"strength":0.7');
    expect(sent).toContain('"note":"downgraded after review"');
    // Before/after audit content rides back through verbatim.
    const body = (await res.json()) as typeof upstream;
    expect(body.from.relationship_type).toBe("equal");
    expect(body.to.strength).toBe(0.7);
    expect(body.mapping_tier).toBe("under_review");
    // Mutable admin resource is never browser-cached.
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("forwards an empty object when the body is not JSON", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "provide at least one field" }), {
        status: 400,
      }),
    );
    const res = await PATCH(badJSONReq() as never, paramsFor("e1"));
    // The upstream's ErrNoFields 400 comes back verbatim.
    expect(res.status).toBe(400);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(String(init.body ?? "")).toBe("{}");
  });

  test("passes the 409 tier-gate refusal through verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error:
            "mapping tier does not permit content edits; demote to under_review via the tier endpoint first",
        }),
        { status: 409 },
      ),
    );
    const res = await PATCH(
      makeReq({ strength: 0.5 }) as never,
      paramsFor("e1"),
    );
    expect(res.status).toBe(409);
  });

  test("passes a non-admin 403 through verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "admin credential required" }), {
        status: 403,
      }),
    );
    const res = await PATCH(
      makeReq({ strength: 0.5 }) as never,
      paramsFor("e1"),
    );
    expect(res.status).toBe(403);
  });
});
