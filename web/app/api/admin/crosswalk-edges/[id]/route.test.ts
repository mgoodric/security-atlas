// Slice 536b — vitest coverage for the crosswalk content-edit BFF route.
//
// Two properties carry the slice's boundaries and are pinned here:
//
//   1. The body is RECONSTRUCTED, not forwarded. `editor_id`,
//      `source_attribution`, `mapping_tier` and the edge endpoints cannot
//      cross this layer even when a caller puts them in the request — which is
//      what keeps invariant #7 and the ADR-0018 provenance axis from depending
//      on the upstream decoder ignoring unknown fields.
//   2. This route never approves anything. It is a PATCH of STRM content; the
//      approve/reject action is the sibling tier route onto slice 483.
//
// The platform validates every field itself and owns the admin gate; these
// tests assert the BFF's own reconstruction, not a replicated authorization.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "@/lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "@/lib/test-utils/test-tokens";

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

const EDGE_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";

type Captured = { url: string; method?: string; body?: string };

function captureUpstream(
  status = 200,
  body: unknown = { edge_id: EDGE_ID, edit_id: "e1" },
): Captured {
  const captured: Captured = { url: "" };
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof input === "string" ? input : input.toString();
      captured.method = init?.method;
      captured.body = init?.body as string | undefined;
      return new Response(JSON.stringify(body), { status });
    },
  );
  return captured;
}

function call(body: unknown, id = EDGE_ID): Promise<Response> {
  return PATCH(
    new Request(`http://bff/api/admin/crosswalk-edges/${id}`, {
      method: "PATCH",
      body: typeof body === "string" ? body : JSON.stringify(body),
    }),
    { params: Promise.resolve({ id }) },
  );
}

const VALID = {
  relationship_type: "subset_of",
  strength: 0.7,
  rationale: "narrowed after reading the anchor text",
  note: "reviewed against ISO 27002 guidance",
};

describe("PATCH /api/admin/crosswalk-edges/[id]", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
  });

  test("forwards a valid edit as PATCH with the four known fields", async () => {
    const captured = captureUpstream();

    const res = await call(VALID);

    expect(res.status).toBe(200);
    expect(captured.method).toBe("PATCH");
    expect(captured.url).toBe(
      `http://atlas:8080/v1/admin/crosswalk-edges/${EDGE_ID}`,
    );
    expect(JSON.parse(captured.body ?? "{}")).toEqual({
      relationship_type: "subset_of",
      strength: 0.7,
      rationale: "narrowed after reading the anchor text",
      note: "reviewed against ISO 27002 guidance",
    });
  });

  test("strips every field outside the four — no smuggled identity or provenance", async () => {
    const captured = captureUpstream();

    await call({
      ...VALID,
      // Each of these would be a boundary violation if it reached upstream.
      editor_id: "00000000-0000-0000-0000-000000000001",
      source_attribution: "scf_official",
      mapping_tier: "verified",
      framework_requirement_id: "00000000-0000-0000-0000-000000000002",
      scf_anchor_id: "00000000-0000-0000-0000-000000000003",
    });

    const forwarded = JSON.parse(captured.body ?? "{}") as Record<
      string,
      unknown
    >;
    expect(Object.keys(forwarded).sort()).toEqual([
      "note",
      "rationale",
      "relationship_type",
      "strength",
    ]);
  });

  test("defaults rationale and note to empty strings when omitted", async () => {
    const captured = captureUpstream();

    await call({ relationship_type: "equal", strength: 1 });

    expect(JSON.parse(captured.body ?? "{}")).toEqual({
      relationship_type: "equal",
      strength: 1,
      rationale: "",
      note: "",
    });
  });

  test("sets Cache-Control: no-store on the mutating response", async () => {
    captureUpstream();
    const res = await call(VALID);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("401s without a bearer cookie and never calls upstream", async () => {
    cookieStore.clear();
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call(VALID);

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("400s on a non-uuid edge id", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call(VALID, "not-a-uuid");

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test.each([
    ["a malformed body", "{not json"],
    ["a JSON array", [1, 2, 3]],
    ["a JSON null", null],
    [
      "an unknown relationship_type",
      { ...VALID, relationship_type: "sort_of" },
    ],
    ["a numeric relationship_type", { ...VALID, relationship_type: 3 }],
    ["a missing relationship_type", { strength: 0.5 }],
    ["a strength above 1", { ...VALID, strength: 1.5 }],
    ["a negative strength", { ...VALID, strength: -0.1 }],
    ["a string strength", { ...VALID, strength: "0.5" }],
    ["a missing strength", { relationship_type: "equal" }],
    ["a non-string rationale", { ...VALID, rationale: { a: 1 } }],
    ["a non-string note", { ...VALID, note: 42 }],
    ["an oversized rationale", { ...VALID, rationale: "x".repeat(4097) }],
    ["an oversized note", { ...VALID, note: "x".repeat(4097) }],
  ])("400s on %s and never calls upstream", async (_label, body) => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call(body);

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("accepts the boundary strengths 0 and 1", async () => {
    for (const strength of [0, 1]) {
      vi.restoreAllMocks();
      const captured = captureUpstream();
      const res = await call({
        relationship_type: "intersects_with",
        strength,
      });
      expect(res.status).toBe(200);
      expect(
        (JSON.parse(captured.body ?? "{}") as { strength: number }).strength,
      ).toBe(strength);
    }
  });

  test("passes the upstream 422 refusals through verbatim", async () => {
    // The two 422s the platform raises: a no-op edit and an edit against a
    // mapping already rejected. The reviewer reads the backend's own wording.
    for (const message of [
      "the edit changes nothing",
      "a rejected mapping cannot be edited",
    ]) {
      vi.restoreAllMocks();
      captureUpstream(422, { error: message });
      const res = await call(VALID);
      expect(res.status).toBe(422);
      expect((await res.json()) as { error: string }).toEqual({
        error: message,
      });
    }
  });

  test("passes an upstream 403 through rather than masking it", async () => {
    captureUpstream(403, { error: "admin credential required" });
    const res = await call(VALID);
    expect(res.status).toBe(403);
  });
});
