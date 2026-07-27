// Slice 536b — vitest coverage for the crosswalk tier-transition BFF route.
//
// This is the approve/reject action, and the upstream is SLICE 483's endpoint.
// The tests that matter are the ones that pin that fact:
//
//   * the forwarded path is `/v1/admin/crosswalk-edges/{id}/tier` — 483's
//     route, not a 536b-owned approval path (the slice's anti-criterion);
//   * `reviewer_id` cannot cross this layer, so the approving identity always
//     comes from the verified admin JWT upstream;
//   * an upstream refusal of an illegal transition passes through unmasked, so
//     the UI can never present a rejected move as an accepted one.
//
// The state machine itself lives in internal/crosswalktier and is covered by
// the Go tier tests; nothing here re-implements or second-guesses it.

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
import { POST } from "./route";

const EDGE_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";

type Captured = { url: string; method?: string; body?: string };

function captureUpstream(status = 200, body: unknown = {}): Captured {
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
  return POST(
    new Request(`http://bff/api/admin/crosswalk-edges/${id}/tier`, {
      method: "POST",
      body: typeof body === "string" ? body : JSON.stringify(body),
    }),
    { params: Promise.resolve({ id }) },
  );
}

describe("POST /api/admin/crosswalk-edges/[id]/tier", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
  });

  test("posts the approval to slice 483's tier route, not a 536b path", async () => {
    const captured = captureUpstream(200, {
      edge_id: EDGE_ID,
      from_tier: "under_review",
      to_tier: "verified",
    });

    const res = await call({ tier: "verified", note: "checked the anchor" });

    expect(res.status).toBe(200);
    expect(captured.method).toBe("POST");
    expect(captured.url).toBe(
      `http://atlas:8080/v1/admin/crosswalk-edges/${EDGE_ID}/tier`,
    );
    expect(JSON.parse(captured.body ?? "{}")).toEqual({
      tier: "verified",
      note: "checked the anchor",
    });
  });

  test("strips reviewer_id and every other field — the approver comes from the JWT", async () => {
    const captured = captureUpstream();

    await call({
      tier: "verified",
      note: "ok",
      reviewer_id: "00000000-0000-0000-0000-000000000001",
      created_at: "1999-01-01T00:00:00Z",
      relationship_type: "equal",
    });

    expect(Object.keys(JSON.parse(captured.body ?? "{}")).sort()).toEqual([
      "note",
      "tier",
    ]);
  });

  test.each([["draft"], ["under_review"], ["verified"], ["rejected"]])(
    "forwards the valid tier %s",
    async (tier) => {
      const captured = captureUpstream();
      await call({ tier });
      expect((JSON.parse(captured.body ?? "{}") as { tier: string }).tier).toBe(
        tier,
      );
    },
  );

  test("defaults an omitted note to an empty string", async () => {
    const captured = captureUpstream();
    await call({ tier: "rejected" });
    expect(JSON.parse(captured.body ?? "{}")).toEqual({
      tier: "rejected",
      note: "",
    });
  });

  test("sets Cache-Control: no-store on the mutating response", async () => {
    captureUpstream();
    const res = await call({ tier: "under_review" });
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("401s without a bearer cookie and never calls upstream", async () => {
    cookieStore.clear();
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call({ tier: "verified" });

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test.each([
    ["a non-uuid id", { tier: "verified" }, "not-a-uuid"],
    ["a malformed body", "{not json", EDGE_ID],
    ["a JSON array", [{ tier: "verified" }], EDGE_ID],
    ["a missing tier", {}, EDGE_ID],
    // `authoritative` is the obsolete source_attribution value from slice 536's
    // superseded approval design (536a §1.2) — it is not a tier and must never
    // reach the platform.
    ["the obsolete 'authoritative' value", { tier: "authoritative" }, EDGE_ID],
    ["a numeric tier", { tier: 2 }, EDGE_ID],
    ["a non-string note", { tier: "verified", note: 7 }, EDGE_ID],
    [
      "an oversized note",
      { tier: "verified", note: "x".repeat(4097) },
      EDGE_ID,
    ],
  ])("400s on %s and never calls upstream", async (_label, body, id) => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call(body, id as string);

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("passes an upstream illegal-transition refusal through unmasked", async () => {
    captureUpstream(422, { error: "illegal tier transition" });

    const res = await call({ tier: "verified" });

    // The UI hides illegal buttons for clarity, but the server is the
    // authority. A refusal must surface as a refusal — never be swallowed into
    // a success the operator would read as an approval that happened.
    expect(res.status).toBe(422);
    expect((await res.json()) as { error: string }).toEqual({
      error: "illegal tier transition",
    });
  });

  test("passes an upstream 403 through rather than masking it", async () => {
    captureUpstream(403, { error: "admin credential required" });
    const res = await call({ tier: "verified" });
    expect(res.status).toBe(403);
  });
});
