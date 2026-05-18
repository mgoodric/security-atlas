// Slice 132 — unit tests for the README-screenshot capture pipeline's
// safety gate. Covers AC-2: the script REFUSES to run unless
// `ATLAS_DEMO_SEED=1` is set AND the upstream HTTP target resolves to a
// loopback or RFC1918 private-range address.
//
// The gate is the load-bearing information-disclosure mitigation per
// slice 132's threat model — a future capture-run operator accidentally
// pointing the script at a live tenant would publish real customer
// data to the README, permanently. The gate fails closed; this test
// suite locks the per-branch behavior so a refactor cannot widen the
// admit set without showing up as a red test.

import { describe, expect, it } from "vitest";

import {
  assertCaptureSafe,
  isLoopbackOrPrivate,
} from "./capture-readme-screenshots";

describe("isLoopbackOrPrivate", () => {
  it("admits localhost name", () => {
    expect(isLoopbackOrPrivate("localhost")).toBe(true);
  });

  it("admits 127.0.0.1", () => {
    expect(isLoopbackOrPrivate("127.0.0.1")).toBe(true);
  });

  it("admits 127.x.x.x loopback range", () => {
    expect(isLoopbackOrPrivate("127.5.10.42")).toBe(true);
  });

  it("admits ::1 IPv6 loopback", () => {
    expect(isLoopbackOrPrivate("::1")).toBe(true);
  });

  it("admits 10.x RFC1918", () => {
    expect(isLoopbackOrPrivate("10.0.0.1")).toBe(true);
    expect(isLoopbackOrPrivate("10.255.255.255")).toBe(true);
  });

  it("admits 172.16-31.x RFC1918", () => {
    expect(isLoopbackOrPrivate("172.16.0.1")).toBe(true);
    expect(isLoopbackOrPrivate("172.31.255.255")).toBe(true);
  });

  it("rejects 172.15.x (just below the RFC1918 range)", () => {
    expect(isLoopbackOrPrivate("172.15.0.1")).toBe(false);
  });

  it("rejects 172.32.x (just above the RFC1918 range)", () => {
    expect(isLoopbackOrPrivate("172.32.0.1")).toBe(false);
  });

  it("admits 192.168.x.x RFC1918", () => {
    expect(isLoopbackOrPrivate("192.168.1.1")).toBe(true);
  });

  it("admits 100.64-127.x carrier-grade NAT range", () => {
    expect(isLoopbackOrPrivate("100.64.0.1")).toBe(true);
    expect(isLoopbackOrPrivate("100.127.255.255")).toBe(true);
  });

  it("admits IPv6 unique-local fc00::/7", () => {
    expect(isLoopbackOrPrivate("fc00::1")).toBe(true);
    expect(isLoopbackOrPrivate("fd12:3456:789a::1")).toBe(true);
  });

  it("rejects public IPv4", () => {
    expect(isLoopbackOrPrivate("8.8.8.8")).toBe(false);
    expect(isLoopbackOrPrivate("1.1.1.1")).toBe(false);
  });

  it("rejects public DNS names", () => {
    expect(isLoopbackOrPrivate("example.com")).toBe(false);
    expect(isLoopbackOrPrivate("atlas.production.tenant.example")).toBe(false);
  });

  it("rejects garbage strings (fails closed)", () => {
    expect(isLoopbackOrPrivate("not-a-host")).toBe(false);
    expect(isLoopbackOrPrivate("")).toBe(false);
  });

  it("admits 0.0.0.0 (treated as loopback alias for local binding)", () => {
    expect(isLoopbackOrPrivate("0.0.0.0")).toBe(true);
  });
});

describe("assertCaptureSafe", () => {
  it("refuses when ATLAS_DEMO_SEED is missing", () => {
    expect(() => assertCaptureSafe({})).toThrow(/ATLAS_DEMO_SEED=1 is required/);
  });

  it("refuses when ATLAS_DEMO_SEED is a typo (true)", () => {
    expect(() => assertCaptureSafe({ ATLAS_DEMO_SEED: "true" })).toThrow(
      /ATLAS_DEMO_SEED=1 is required/,
    );
  });

  it("refuses when ATLAS_DEMO_SEED is a typo (yes)", () => {
    expect(() => assertCaptureSafe({ ATLAS_DEMO_SEED: "yes" })).toThrow(
      /ATLAS_DEMO_SEED=1 is required/,
    );
  });

  it("refuses when ATLAS_DEMO_SEED is empty string", () => {
    expect(() => assertCaptureSafe({ ATLAS_DEMO_SEED: "" })).toThrow(
      /ATLAS_DEMO_SEED=1 is required/,
    );
  });

  it("admits when ATLAS_DEMO_SEED=1 and ATLAS_HTTP_URL defaults to localhost", () => {
    expect(() => assertCaptureSafe({ ATLAS_DEMO_SEED: "1" })).not.toThrow();
  });

  it("admits ATLAS_HTTP_URL=http://127.0.0.1:8080", () => {
    expect(() =>
      assertCaptureSafe({
        ATLAS_DEMO_SEED: "1",
        ATLAS_HTTP_URL: "http://127.0.0.1:8080",
      }),
    ).not.toThrow();
  });

  it("admits ATLAS_HTTP_URL=http://10.0.0.5:8080", () => {
    expect(() =>
      assertCaptureSafe({
        ATLAS_DEMO_SEED: "1",
        ATLAS_HTTP_URL: "http://10.0.0.5:8080",
      }),
    ).not.toThrow();
  });

  it("refuses ATLAS_HTTP_URL pointing at a public hostname", () => {
    expect(() =>
      assertCaptureSafe({
        ATLAS_DEMO_SEED: "1",
        ATLAS_HTTP_URL: "https://atlas.production.example.com",
      }),
    ).toThrow(/not a loopback or RFC1918 private address/);
  });

  it("refuses ATLAS_HTTP_URL pointing at a public IPv4", () => {
    expect(() =>
      assertCaptureSafe({
        ATLAS_DEMO_SEED: "1",
        ATLAS_HTTP_URL: "http://8.8.8.8:80",
      }),
    ).toThrow(/not a loopback or RFC1918 private address/);
  });

  it("refuses malformed ATLAS_HTTP_URL (fails closed)", () => {
    expect(() =>
      assertCaptureSafe({
        ATLAS_DEMO_SEED: "1",
        ATLAS_HTTP_URL: "not-a-url",
      }),
    ).toThrow(/not a loopback or RFC1918 private address/);
  });

  it("error message cites slice 132 + the recipe", () => {
    try {
      assertCaptureSafe({});
      throw new Error("should have thrown");
    } catch (err) {
      expect((err as Error).message).toContain("slice 132");
      expect((err as Error).message).toContain("refresh-screenshots");
    }
  });
});
