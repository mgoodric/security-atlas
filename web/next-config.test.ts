// Slice 208 — vitest regression covering the `async rewrites()` shape
// in web/next.config.ts.
//
// Why: the next.config.ts module is consumed by Next.js itself at build
// time + dev-server start. A typo in the rewrite source/destination
// shape (e.g. dropping the :path* param, swapping source/destination,
// missing the leading slash) compiles cleanly but breaks the dashboard
// at runtime. This spec asserts the three-rule contract end-to-end.
//
// AC-3 from the slice doc: with ATLAS_HTTP_URL set, rewrites() returns
// the expected 3-rule array; with the env unset, rewrites() falls back
// to http://localhost:8080 AND emits a console.warn (D1).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

// next.config.ts evaluates its top-level constants at module-load. To
// exercise both the env-set and env-unset paths within one suite we
// re-import the module via dynamic import after mutating process.env.
// vitest's resetModules clears the module-cache between cases so each
// import picks up the env mutation.

const ENV_KEY = "ATLAS_HTTP_URL";

describe("next.config rewrites (slice 208)", () => {
  let savedEnv: string | undefined;
  let warnSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    savedEnv = process.env[ENV_KEY];
    warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.resetModules();
  });

  afterEach(() => {
    if (savedEnv === undefined) {
      delete process.env[ENV_KEY];
    } else {
      process.env[ENV_KEY] = savedEnv;
    }
    warnSpy.mockRestore();
  });

  test("returns the three expected rewrite rules when ATLAS_HTTP_URL is set", async () => {
    process.env[ENV_KEY] = "http://atlas:8080";

    const mod = await import("./next.config");
    const config = mod.default;

    expect(typeof config.rewrites).toBe("function");
    const rules = await config.rewrites!();

    // The slice contract: an array, NOT a `{ beforeFiles, afterFiles,
    // fallback }` object. Three rules. Sources are exact-path-prefix
    // and well-known.
    expect(Array.isArray(rules)).toBe(true);
    expect(rules).toEqual([
      { source: "/v1/:path*", destination: "http://atlas:8080/v1/:path*" },
      { source: "/health", destination: "http://atlas:8080/health" },
      { source: "/metrics", destination: "http://atlas:8080/metrics" },
    ]);

    // D1: when ATLAS_HTTP_URL is set, no warning fires — production /
    // docker-compose deploys are silent.
    expect(warnSpy).not.toHaveBeenCalled();
  });

  test("falls back to http://localhost:8080 + emits one console.warn when ATLAS_HTTP_URL is unset", async () => {
    delete process.env[ENV_KEY];

    const mod = await import("./next.config");
    const config = mod.default;

    const rules = await config.rewrites!();

    expect(rules).toEqual([
      {
        source: "/v1/:path*",
        destination: "http://localhost:8080/v1/:path*",
      },
      { source: "/health", destination: "http://localhost:8080/health" },
      { source: "/metrics", destination: "http://localhost:8080/metrics" },
    ]);

    // D1: exactly one warn fires per rewrites() invocation when the
    // fallback path runs. (Next.js calls rewrites() once per build /
    // dev-server start, so this is a per-process notice, not a hot-loop
    // log spam concern.)
    expect(warnSpy).toHaveBeenCalledTimes(1);
    const message = warnSpy.mock.calls[0]?.[0] as string;
    expect(message).toContain("ATLAS_HTTP_URL");
    expect(message).toContain("http://localhost:8080");
  });

  test("treats empty-string ATLAS_HTTP_URL as unset (falls back + warns)", async () => {
    // Defensive: an operator who sets ATLAS_HTTP_URL= (empty) should
    // get the fallback, not an empty-string destination that would
    // build `://v1/:path*` (an invalid URL).
    process.env[ENV_KEY] = "";

    const mod = await import("./next.config");
    const config = mod.default;
    const rules = (await config.rewrites!()) as Array<{
      source: string;
      destination: string;
    }>;

    expect(rules[0]).toEqual({
      source: "/v1/:path*",
      destination: "http://localhost:8080/v1/:path*",
    });
    expect(warnSpy).toHaveBeenCalledTimes(1);
  });

  test("does NOT rewrite /api/* or /oauth/* (P0-A1 + P0-A2)", async () => {
    process.env[ENV_KEY] = "http://atlas:8080";
    const mod = await import("./next.config");
    const rules = await mod.default.rewrites!();
    const sources = (rules as Array<{ source: string }>).map((r) => r.source);

    // P0-A1: BFF routes stay server-side.
    expect(sources).not.toContain("/api/:path*");
    expect(sources).not.toContain("/api/:path");
    // P0-A2: OIDC callback stays server-side.
    expect(sources).not.toContain("/oauth/:path*");
    expect(sources).not.toContain("/oauth/:path");
    // Defense in depth: catch-all rewrites would be a footgun.
    expect(sources).not.toContain("/:path*");
  });

  test("preserves output: 'standalone' (slice 037 invariant)", async () => {
    process.env[ENV_KEY] = "http://atlas:8080";
    const mod = await import("./next.config");
    expect(mod.default.output).toBe("standalone");
  });
});
