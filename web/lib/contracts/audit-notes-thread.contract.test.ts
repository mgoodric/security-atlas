// Slice 689 — contract-test-tier rollout (consumer side: GET
// /v1/audit-notes/thread, the Audit Hub visible-thread read).
//
// PROVIDER: internal/api/auditnotes/contractrecord_test.go records the real
// Thread handler's bodies into audit-notes-thread.golden.json. This CONSUMER
// half asserts the BFF (web/app/api/audit/audit-notes/thread/route.ts) against
// them. The BFF rebuilds the upstream query string from the request's
// searchParams but forwards the upstream body text unchanged — a VERBATIM
// passthrough — so the assert is toEqual(golden).
//
// P0-2: the platform filters auditor_only notes to their author at the QUERY
// LAYER; this BFF passes the upstream response through verbatim and the UI
// never client-side-filters visibility. The wire shape this golden pins IS the
// caller's full picture.
//
// Load-bearing field assumptions:
//   * audit_notes is ALWAYS an array (never null); empty thread -> []
//   * count is a number; each note carries string id/audit_period_id/
//     author_user_id/scope_type/body/visibility + string created_at/updated_at
//   * parent_note_id + depth are present on replies (omitempty on root notes)

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";

import { GET } from "../../app/api/audit/audit-notes/thread/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "audit-notes-thread.golden.json"), "utf8"),
) as Golden;

const PERIOD_ID = "11111111-1111-4111-8111-111111111111";
const SCOPE_ID = "22222222-2222-4222-8222-222222222222";

// The thread BFF reads its filters from req.nextUrl.searchParams. A minimal
// stub carrying a real URLSearchParams is all the route touches.
function threadRequest(): unknown {
  const params = new URLSearchParams({
    audit_period_id: PERIOD_ID,
    scope_type: "control",
    scope_id: SCOPE_ID,
  });
  return { nextUrl: { searchParams: params } };
}

describe("contract: GET /api/audit/audit-notes/thread <-> atlas GET /v1/audit-notes/thread", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/audit-notes/thread");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant carries audit_notes[] + a numeric count", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(
        Array.isArray(body.audit_notes),
        `${name}.audit_notes must be an array`,
      ).toBe(true);
      for (const n of body.audit_notes as Record<string, unknown>[]) {
        expect(typeof n.id, `${name}.note.id`).toBe("string");
        expect(typeof n.audit_period_id, `${name}.note.audit_period_id`).toBe(
          "string",
        );
        expect(typeof n.author_user_id, `${name}.note.author_user_id`).toBe(
          "string",
        );
        expect(typeof n.scope_type, `${name}.note.scope_type`).toBe("string");
        expect(typeof n.body, `${name}.note.body`).toBe("string");
        expect(typeof n.visibility, `${name}.note.visibility`).toBe("string");
        expect(typeof n.created_at, `${name}.note.created_at`).toBe("string");
        expect(typeof n.updated_at, `${name}.note.updated_at`).toBe("string");
      }
    }
  });

  test("empty thread records audit_notes:[] + count 0 (never null)", () => {
    const empty = golden.variants.empty;
    expect(empty.audit_notes).toEqual([]);
    expect(empty.count).toBe(0);
  });

  test("reply note carries parent_note_id + depth pinning the thread structure", () => {
    const notes = golden.variants.populated.audit_notes as Record<
      string,
      unknown
    >[];
    const reply = notes.find((n) => n.parent_note_id !== undefined);
    expect(reply, "a reply with parent_note_id is present").toBeDefined();
    expect(typeof reply?.parent_note_id).toBe("string");
    expect(typeof reply?.depth).toBe("number");
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
      const providerBody = golden.variants[variantName];
      vi.spyOn(globalThis, "fetch").mockImplementation(
        async () =>
          new Response(JSON.stringify(providerBody), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );

      const res = await GET(threadRequest() as never);
      expect(res.status).toBe(200);
      const got = (await res.json()) as Record<string, unknown>;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 400 when required filters are absent (guard before upstream)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const bad = { nextUrl: { searchParams: new URLSearchParams() } };
    const res = await GET(bad as never);
    expect(res.status).toBe(400);
  });
});
