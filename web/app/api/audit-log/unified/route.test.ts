// Slice 125 — vitest coverage for the /api/audit-log/unified BFF route.
//
// AC-7. The BFF is a narrow forwarder over the slice-124
// `GET /v1/admin/audit-log/unified` endpoint. Behavior under test:
//
//   * No `SESSION_COOKIE` (post-slice-206: `atlas_jwt`) cookie -> 401 { error }.
//   * Bearer present, params present -> upstream fetch carries
//     `Authorization: Bearer <bearer>` and the full query string.
//   * Backend 400 (missing from/to or malformed) -> pass-through unchanged.
//   * Backend 403 (non-admin/auditor caller) -> pass-through unchanged.
//   * Backend 200 happy-path -> pass-through JSON body unchanged.
//
// Slice 110 P0-A2: this route MUST NOT forward the `atlas_session` cookie.
// The unified-audit-log handler authenticates on the bearer alone; broadening
// the cookie surface would violate slice 110's narrow-scope rule.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { SESSION_COOKIE, OIDC_SESSION_COOKIE } from "@/lib/auth";
import { GET } from "./route";

function makeReq(query: string): Request {
  return new Request(`http://test/api/audit-log/unified${query}`);
}

describe("GET /api/audit-log/unified", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await GET(
      makeReq("?from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBeDefined();
  });

  test("forwards bearer + query string verbatim on happy path", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          entries: [
            {
              occurred_at: "2026-05-17T12:00:00Z",
              actor_id: "00000000-0000-0000-0000-000000001111",
              tenant_id: "00000000-0000-0000-0000-00000000d3a0",
              kind: "evidence",
              target_type: "evidence_record",
              target_id: "abc",
              action: "push",
              row_id: "11111111-1111-1111-1111-111111111111",
              payload_json: {},
            },
          ],
          next_cursor: "",
        }),
        { status: 200 },
      ),
    );

    const query =
      "?from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z&kind=evidence,me";
    const res = await GET(makeReq(query));
    expect(res.status).toBe(200);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0];
    const requestedURL = String(call[0]);
    expect(requestedURL).toBe(
      `http://atlas:8080/v1/admin/audit-log/unified${query}`,
    );

    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers).toBeDefined();
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    // P0 — the unified audit-log endpoint authenticates on the bearer alone.
    // Slice 110 forbids broadening the atlas_session cookie surface.
    expect(headers?.Cookie).toBeUndefined();

    const body = (await res.json()) as { entries: unknown[] };
    expect(Array.isArray(body.entries)).toBe(true);
    expect(body.entries).toHaveLength(1);
  });

  test("passes through 400 from backend when window is invalid", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "window exceeds 90 days; narrow the from/to range",
        }),
        { status: 400 },
      ),
    );

    // 200-day window — backend rejects with 400; BFF passes through.
    const res = await GET(
      makeReq("?from=2025-11-01T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/window exceeds 90 days/);
  });

  test("passes through 400 from backend when from/to are missing", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "from query parameter is required (RFC3339)" }),
        {
          status: 400,
        },
      ),
    );

    const res = await GET(makeReq(""));
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/from query parameter is required/);
  });

  test("passes through 403 from backend (non-admin caller)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "admin, auditor, or grc_engineer role required",
        }),
        { status: 403 },
      ),
    );

    const res = await GET(
      makeReq("?from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/admin, auditor/);
  });

  test("ignores atlas_session cookie when present (slice 110 P0-A2)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    cookieStore.set(OIDC_SESSION_COOKIE, "test-atlas-session-id");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ entries: [], next_cursor: "" }), {
        status: 200,
      }),
    );

    const res = await GET(
      makeReq("?from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(200);
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    // Cookie MUST NOT be forwarded — surface-area narrow-scope rule.
    expect(headers?.Cookie).toBeUndefined();
  });
});
