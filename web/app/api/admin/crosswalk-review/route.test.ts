// Slice 536b — vitest coverage for the crosswalk review-queue BFF route.
//
// The load-bearing property here is the RECONSTRUCTED query string: the route
// forwards only the five parameters the upstream defines, each shape-checked,
// so arbitrary client text never reaches the platform's query parser. The
// tests below pin that reconstruction (including the negative cases, which are
// the ones a refactor would quietly drop) plus the unauthenticated short
// circuit inherited from forwardJSON.
//
// What these tests deliberately do NOT assert: the admin gate. The BFF does not
// replicate it — the platform's requireAdmin is the authority and is covered by
// the Go side (admincrosswalkreview TestQueueNonAdminRejected).

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
import { GET } from "./route";

const VERSION_ID = "11111111-2222-3333-4444-555555555555";

// captureUpstream installs a fetch spy that records the forwarded URL and
// returns `body` as a 200. Returns a getter for the captured URL.
function captureUpstream(body: unknown = { requirements: [] }): () => string {
  let captured = "";
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL) => {
      captured = typeof input === "string" ? input : input.toString();
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    },
  );
  return () => captured;
}

function call(query: string): Promise<Response> {
  return GET(new Request(`http://bff/api/admin/crosswalk-review?${query}`));
}

describe("GET /api/admin/crosswalk-review", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
  });

  test("forwards the framework_version_id and passes the upstream body through", async () => {
    const url = captureUpstream({
      framework_version_id: VERSION_ID,
      requirements: [],
      conflict_count: 0,
      total: 0,
    });

    const res = await call(`framework_version_id=${VERSION_ID}`);

    expect(res.status).toBe(200);
    expect(url()).toBe(
      `http://atlas:8080/v1/admin/crosswalk-review?framework_version_id=${VERSION_ID}`,
    );
    const body = (await res.json()) as { framework_version_id: string };
    expect(body.framework_version_id).toBe(VERSION_ID);
  });

  test("sends the bearer cookie as an Authorization header and never as a cookie", async () => {
    let headers: Headers | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        headers = new Headers(init?.headers);
        return new Response("{}", { status: 200 });
      },
    );

    await call(`framework_version_id=${VERSION_ID}`);

    expect(headers?.get("Authorization")).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
    expect(headers?.get("Cookie")).toBeNull();
  });

  test("401s without a bearer cookie and never calls upstream", async () => {
    cookieStore.clear();
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call(`framework_version_id=${VERSION_ID}`);

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test.each([
    ["absent", ""],
    ["not a uuid", "framework_version_id=not-a-uuid"],
    ["empty", "framework_version_id="],
    ["sql-ish text", "framework_version_id=1%20OR%201%3D1"],
  ])("400s when framework_version_id is %s", async (_label, query) => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call(query);

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards a valid tier filter and drops an invalid one", async () => {
    const url = captureUpstream();
    await call(`framework_version_id=${VERSION_ID}&tier=under_review`);
    expect(url()).toContain("tier=under_review");

    vi.restoreAllMocks();
    const url2 = captureUpstream();
    await call(`framework_version_id=${VERSION_ID}&tier=authoritative`);
    // `authoritative` is the obsolete source_attribution value from slice 536's
    // superseded approval design (536a §1.2). It is not a tier and must never
    // reach the platform's enum parser.
    expect(url2()).not.toContain("tier=");
  });

  test("forwards conflicts_only only for the literal string 'true'", async () => {
    const url = captureUpstream();
    await call(`framework_version_id=${VERSION_ID}&conflicts_only=true`);
    expect(url()).toContain("conflicts_only=true");

    vi.restoreAllMocks();
    const url2 = captureUpstream();
    await call(`framework_version_id=${VERSION_ID}&conflicts_only=1`);
    expect(url2()).not.toContain("conflicts_only");
  });

  test.each([
    ["a valid pair", "limit=25&offset=50", ["limit=25", "offset=50"]],
    ["a negative limit", "limit=-5", []],
    ["a non-numeric limit", "limit=abc", []],
    ["an over-max limit", "limit=5000", []],
    ["a negative offset", "offset=-1", []],
  ])("pagination — %s", async (_label, query, expected) => {
    const url = captureUpstream();
    await call(`framework_version_id=${VERSION_ID}&${query}`);
    const forwarded = url();
    if (expected.length === 0) {
      expect(forwarded).toBe(
        `http://atlas:8080/v1/admin/crosswalk-review?framework_version_id=${VERSION_ID}`,
      );
    } else {
      for (const fragment of expected) expect(forwarded).toContain(fragment);
    }
  });

  test("drops unknown query parameters entirely", async () => {
    const url = captureUpstream();

    await call(
      `framework_version_id=${VERSION_ID}&tenant_id=someone-else&order_by=1;DROP`,
    );

    // The reconstruction — not a passthrough of url.search — is what makes this
    // hold. tenant_id in particular must never reach a catalog route.
    expect(url()).toBe(
      `http://atlas:8080/v1/admin/crosswalk-review?framework_version_id=${VERSION_ID}`,
    );
  });

  test("passes an upstream error status and body through verbatim", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "admin credential required" }), {
        status: 403,
      }),
    );

    const res = await call(`framework_version_id=${VERSION_ID}`);

    expect(res.status).toBe(403);
    expect((await res.json()) as { error: string }).toEqual({
      error: "admin credential required",
    });
  });
});
