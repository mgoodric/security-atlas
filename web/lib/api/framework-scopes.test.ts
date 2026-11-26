// #2115 — the framework-scope server client passes the platform's refusal
// through: the scope page shows it as the platform words it.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { APIError } from "./base";
import {
  createFrameworkScope,
  patchFrameworkScopePredicate,
  transitionFrameworkScope,
} from "./framework-scopes";

const ID = "00000000-0000-0000-0000-000000002115";

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    statusText: status === 403 ? "Forbidden" : "Conflict",
    headers: { "Content-Type": "application/json" },
  });
}

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("framework-scope errors", () => {
  it("approve carries the approval rule's message and status", async () => {
    const msg =
      "frameworkscope: the submitter cannot approve their own scope; a second approver must approve it";
    fetchMock.mockResolvedValueOnce(jsonResponse({ error: msg }, 403));
    const err = await transitionFrameworkScope("b", ID, "approve").catch(
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(403);
    expect((err as APIError).message).toBe(msg);
  });

  it("create and patch carry the message too", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: "predicate is required" }, 409),
    );
    await expect(
      createFrameworkScope("b", {
        framework_version_id: ID,
        name: "n",
        predicate: { op: "true" },
      }),
    ).rejects.toThrow("predicate is required");
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: "scope is frozen" }, 409),
    );
    await expect(
      patchFrameworkScopePredicate("b", ID, { op: "true" }),
    ).rejects.toThrow("scope is frozen");
  });

  it("a 5xx shows the status line, never its {error} body (#2433)", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "pq: relation framework_scopes_x missing" }),
        { status: 500, statusText: "Internal Server Error" },
      ),
    );
    const err = await transitionFrameworkScope("b", ID, "approve").catch(
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(500);
    expect((err as APIError).message).toBe("500 Internal Server Error");
  });

  it("falls back to the status line for a body that is not JSON", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response("<html>", { status: 403, statusText: "Forbidden" }),
    );
    await expect(transitionFrameworkScope("b", ID, "submit")).rejects.toThrow(
      "403 Forbidden",
    );
  });
});
