// #2432 — the entity picker's server-side evidence search. Pure wire
// tests over a mocked global fetch: the request it sends, the hits it
// keeps, and a search refusal surfaced as the 403 it means.

import { afterEach, describe, expect, test, vi } from "vitest";

import { APIError } from "./base";
import { EVIDENCE_SEARCH_LIMIT, searchEvidence } from "./evidence";

const realFetch = globalThis.fetch;

function respond(body: unknown, status = 200) {
  const calls: string[] = [];
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
    calls.push(String(url));
    return new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof fetch;
  return calls;
}

afterEach(() => {
  globalThis.fetch = realFetch;
});

describe("searchEvidence", () => {
  test("asks unified search for evidence only, and keeps evidence hits", async () => {
    const calls = respond({
      hits: [
        { id: "e1", type: "evidence", title: "legacy.backup · BCD-11" },
        { id: "c1", type: "controls", title: "Backup" },
      ],
      partial_types: [],
    });
    expect(await searchEvidence("  legacy ")).toEqual([
      { id: "e1", title: "legacy.backup · BCD-11" },
    ]);
    const url = new URL(calls[0], "http://x");
    expect(url.pathname).toBe("/api/search");
    expect(url.searchParams.get("q")).toBe("legacy");
    expect(url.searchParams.get("types")).toBe("evidence");
    expect(url.searchParams.get("limit")).toBe(String(EVIDENCE_SEARCH_LIMIT));
  });

  test("evidence in partial_types is a refusal: a 403, and no hits", async () => {
    respond({ hits: [], partial_types: ["evidence"] });
    const err = await searchEvidence("legacy").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(403);
  });

  test("an upstream error keeps its status", async () => {
    respond({ error: "forbidden" }, 403);
    const err = await searchEvidence("legacy").catch((e: unknown) => e);
    expect((err as APIError).status).toBe(403);
  });
});
