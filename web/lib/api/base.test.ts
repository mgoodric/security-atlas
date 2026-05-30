// Slice 069 — vitest seed coverage for lib/api/base.ts URL resolution.
// (Slice 370 split the api god-file into per-domain modules; slice 396
// retired the `lib/api.ts` barrel, so this test imports `apiBaseURL`
// from `./base` directly — its real home — instead of the old barrel.)
//
// The function under test (`apiBaseURL`) picks the right HTTP base URL
// per execution context:
//
//   * Server (typeof window === "undefined"):
//       ATLAS_HTTP_URL  > NEXT_PUBLIC_API_BASE_URL > "http://atlas:8080"
//   * Client (typeof window defined):
//       NEXT_PUBLIC_API_BASE_URL > ""  (same-origin)
//
// These tests guard the slice-005 / slice-072 (RFC PR #95) deployment
// contract: same image, different URLs per side of the network.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { apiBaseURL } from "./base";

describe("apiBaseURL — server context (typeof window === undefined)", () => {
  // The test runtime is `node` (see vitest.config.ts) → `window` is
  // undefined by default, so we are already in the server branch.

  const originalATLAS = process.env.ATLAS_HTTP_URL;
  const originalPUBLIC = process.env.NEXT_PUBLIC_API_BASE_URL;

  beforeEach(() => {
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
  });

  afterEach(() => {
    if (originalATLAS === undefined) {
      delete process.env.ATLAS_HTTP_URL;
    } else {
      process.env.ATLAS_HTTP_URL = originalATLAS;
    }
    if (originalPUBLIC === undefined) {
      delete process.env.NEXT_PUBLIC_API_BASE_URL;
    } else {
      process.env.NEXT_PUBLIC_API_BASE_URL = originalPUBLIC;
    }
  });

  test("ATLAS_HTTP_URL takes precedence when set", () => {
    process.env.ATLAS_HTTP_URL = "http://atlas-internal:9090";
    process.env.NEXT_PUBLIC_API_BASE_URL = "https://api.example.com";
    expect(apiBaseURL()).toBe("http://atlas-internal:9090");
  });

  test("falls back to NEXT_PUBLIC_API_BASE_URL when ATLAS_HTTP_URL unset", () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "https://api.example.com";
    expect(apiBaseURL()).toBe("https://api.example.com");
  });

  test("falls back to http://atlas:8080 when both env vars unset", () => {
    expect(apiBaseURL()).toBe("http://atlas:8080");
  });
});

describe("apiBaseURL — client context (typeof window defined)", () => {
  // Simulate the browser by defining a `window` global. We restore
  // afterward so the server tests above stay independent.
  const originalPUBLIC = process.env.NEXT_PUBLIC_API_BASE_URL;

  beforeEach(() => {
    vi.stubGlobal("window", {});
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    if (originalPUBLIC === undefined) {
      delete process.env.NEXT_PUBLIC_API_BASE_URL;
    } else {
      process.env.NEXT_PUBLIC_API_BASE_URL = originalPUBLIC;
    }
  });

  test("returns NEXT_PUBLIC_API_BASE_URL when set", () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = "https://api.example.com";
    expect(apiBaseURL()).toBe("https://api.example.com");
  });

  test("returns empty string (same-origin) when NEXT_PUBLIC_API_BASE_URL unset", () => {
    expect(apiBaseURL()).toBe("");
  });
});
