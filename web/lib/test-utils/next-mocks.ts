// web/lib/test-utils/next-mocks.ts
//
// Slice 348 V-3 — Shared vitest mock factory for `next/server`.
//
// 46 vitest test files were re-declaring the same `NextResponse`
// stand-in in their own `vi.mock("next/server", ...)` blocks (~15 LOC
// per file). The mock body is identical: a Response subclass with a
// static `.json()` helper that respects `init.status` and
// `init.headers`. Slice 334 V-3 (Medium) named this duplication; this
// helper centralizes it.
//
// How to use it (preserves vitest hoisting semantics):
//
//   import { vi } from "vitest";
//   import { mockNextServer } from "@/lib/test-utils/next-mocks";
//
//   vi.mock("next/server", () => mockNextServer());
//
// vitest hoists `vi.mock` calls to the top of the module, so the
// `mockNextServer()` call body runs before any import of the module
// under test. The helper itself is a plain function (no top-level
// side effects) which is the shape vi.mock's factory expects.
//
// What the helper guarantees:
//   * `NextResponse` extends the global `Response`.
//   * `NextResponse.json(body, init?)` serializes `body` to JSON,
//     respects `init.status` (defaults to 200), and merges
//     `init.headers` on top of `Content-Type: application/json`.
//   * No client-cookie / route-segment behavior is mocked — those
//     surfaces have their own mock helpers under `next/headers` /
//     `next/navigation`. This helper covers ONLY the `next/server`
//     surface that route-handler tests touch.
//
// What this helper does NOT cover (yet):
//   * `NextRequest` — no current test re-declares it; if a future
//     test needs it, extend this helper with `NextRequest` and update
//     the existing 46 sites in the same PR.
//   * Streaming-response variants — none of the audited 46 sites use
//     them; if a future handler test needs `new Response(stream)`,
//     the same helper works (NextResponse extends Response so streams
//     pass through the standard Response constructor).

export type NextMocks = {
  NextResponse: typeof Response & {
    json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): Response;
  };
  NextRequest: typeof Request;
};

export function mockNextServer(): NextMocks {
  class NextResponse extends Response {
    static json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): NextResponse {
      // The two variants the sweep folded into one shape:
      //
      //   1. Plain: `JSON.stringify(body)` — works for objects, arrays,
      //      strings, numbers, booleans. `JSON.stringify(null)` returns
      //      the string `"null"`.
      //   2. Null-safe: `body === null ? "null" : JSON.stringify(body)`
      //      — explicit branch to make the null case discoverable in
      //      diffs. Behaviorally identical to (1) because
      //      `JSON.stringify(null) === "null"`, but the explicit branch
      //      survived as the dominant export-test shape.
      //
      // Helper adopts the explicit branch so the diff against the
      // sites that had it shows zero behavior change.
      const payload = body === null ? "null" : JSON.stringify(body);
      return new NextResponse(payload, {
        status: init?.status ?? 200,
        headers: {
          "Content-Type": "application/json",
          ...(init?.headers ?? {}),
        },
      });
    }
  }
  // NextRequest is a thin Request subclass for tests that destructure
  // `import { NextRequest, NextResponse } from "next/server"`. Four
  // of the 46 swept files used this shape (dynamic-route handler
  // tests). Adding the class to the helper lets those files use the
  // same `vi.mock("next/server", () => mockNextServer())` line as
  // the response-only tests.
  class NextRequest extends Request {}
  return {
    NextResponse: NextResponse as unknown as NextMocks["NextResponse"],
    NextRequest: NextRequest as unknown as NextMocks["NextRequest"],
  };
}
