// #1778 — direct coverage for the internal fetch helpers. bffPostJSON
// moved here from control-imports.ts, which dropped this file below its
// slice-347 coverage floor; these tests cover every helper directly
// rather than through the domain modules that call them.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { apiFetch, bffControlFetch, bffPostJSON } from "./_shared";
import { APIError } from "./base";

const json = (body: unknown, status = 200, statusText = "") =>
  new Response(JSON.stringify(body), { status, statusText });

describe("internal fetch helpers", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("apiFetch", () => {
    test("sends the bearer, never caches, returns the response", async () => {
      const spy = vi
        .spyOn(globalThis, "fetch")
        .mockResolvedValueOnce(json({ ok: true }));
      const res = await apiFetch("/v1/x", "b-1");
      expect(await res.json()).toEqual({ ok: true });
      expect(String(spy.mock.calls[0]?.[0])).toBe("http://atlas:8080/v1/x");
      const init = spy.mock.calls[0]?.[1] as RequestInit;
      expect((init.headers as Record<string, string>).Authorization).toBe(
        "Bearer b-1",
      );
      expect(init.cache).toBe("no-store");
    });

    test("a non-2xx throws an APIError with the status", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        json({}, 404, "Not Found"),
      );
      await expect(apiFetch("/v1/x", "b")).rejects.toMatchObject({
        status: 404,
        message: "404 Not Found",
      });
    });
  });

  describe("bffControlFetch", () => {
    test("returns the parsed body", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(json({ n: 1 }));
      await expect(bffControlFetch("/api/x")).resolves.toEqual({ n: 1 });
    });

    test("unwraps an {error} body into the APIError message", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        json({ error: "forbidden" }, 403),
      );
      const err = await bffControlFetch("/api/x").catch((e: unknown) => e);
      expect(err).toBeInstanceOf(APIError);
      expect(err).toMatchObject({ status: 403, message: "forbidden" });
    });

    test("keeps the status line when the body is not JSON", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response("<html>", { status: 502, statusText: "Bad Gateway" }),
      );
      await expect(bffControlFetch("/api/x")).rejects.toMatchObject({
        status: 502,
        message: "502 Bad Gateway",
      });
    });
  });

  describe("bffPostJSON", () => {
    test("posts a JSON body and returns the parsed response", async () => {
      const spy = vi
        .spyOn(globalThis, "fetch")
        .mockResolvedValueOnce(json({ decision: "approved" }));
      await expect(
        bffPostJSON("/api/x", { scf_id: "IAC-10" }),
      ).resolves.toEqual({ decision: "approved" });
      const init = spy.mock.calls[0]?.[1] as RequestInit;
      expect(init.method).toBe("POST");
      expect(init.body).toBe(JSON.stringify({ scf_id: "IAC-10" }));
      expect((init.headers as Record<string, string>)["Content-Type"]).toBe(
        "application/json",
      );
    });

    test("no body: no content type", async () => {
      const spy = vi
        .spyOn(globalThis, "fetch")
        .mockResolvedValueOnce(json({ approved_count: 0 }));
      await bffPostJSON("/api/x");
      const init = spy.mock.calls[0]?.[1] as RequestInit;
      expect(init.body).toBeUndefined();
      expect(init.headers).toBeUndefined();
    });

    test("a non-2xx throws the platform's {error} message", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        json({ error: "proposal already decided" }, 409),
      );
      await expect(bffPostJSON("/api/x", {})).rejects.toMatchObject({
        status: 409,
        message: "proposal already decided",
      });
    });

    test("a non-JSON error body falls back to the status", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response("oops", { status: 500 }),
      );
      await expect(bffPostJSON("/api/x", {})).rejects.toMatchObject({
        status: 500,
        message: "request failed (500)",
      });
    });
  });
});
