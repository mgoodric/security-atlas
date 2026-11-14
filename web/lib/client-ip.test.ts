// #2040 — the BFF forwards the browser's IP from its own trusted chain,
// never a browser-supplied X-Forwarded-For.
//
// The wire-level cases load web/peer-chain.cjs (the production preload)
// and drive a real node http.Server over loopback, so the socket peer the
// chain ends in is real, not hand-built.

import { createRequire } from "node:module";
import http from "node:http";
import type { AddressInfo } from "node:net";

import { afterAll, beforeAll, describe, expect, it } from "vitest";

import {
  PEER_CHAIN_HEADER,
  forwardedForHeaders,
  parseTrustedProxies,
  resolveClientIP,
} from "./client-ip";

function trusted(cidrs: string) {
  const list = parseTrustedProxies(cidrs);
  if (!list) throw new Error(`bad cidrs ${cidrs}`);
  return list;
}

describe("resolveClientIP", () => {
  it("returns the peer when no proxy is trusted, ignoring forged hops", () => {
    expect(resolveClientIP("6.6.6.6, 198.51.100.7", trusted(""))).toBe(
      "198.51.100.7",
    );
  });

  it("steps over trusted proxies to the first untrusted hop", () => {
    expect(
      resolveClientIP("6.6.6.6, 198.51.100.7, 10.0.0.5", trusted("10.0.0.0/8")),
    ).toBe("198.51.100.7");
  });

  it("unwraps an IPv4-mapped peer before matching", () => {
    expect(
      resolveClientIP("203.0.113.9, ::ffff:10.0.0.5", trusted("10.0.0.0/8")),
    ).toBe("203.0.113.9");
  });

  it("matches IPv6 proxies", () => {
    expect(resolveClientIP("2001:db8::1, fd00::2", trusted("fd00::/8"))).toBe(
      "2001:db8::1",
    );
  });

  it("stops at a malformed hop with the closest attributable address", () => {
    expect(resolveClientIP("nonsense, 10.0.0.5", trusted("10.0.0.0/8"))).toBe(
      "10.0.0.5",
    );
  });

  it("refuses an empty or oversized chain", () => {
    expect(resolveClientIP("", trusted(""))).toBeNull();
    const long = Array.from({ length: 60 }, () => "10.0.0.1").join(", ");
    expect(resolveClientIP(long, trusted("10.0.0.0/8"))).toBeNull();
  });
});

describe("parseTrustedProxies", () => {
  it.each(["10.0.0.0", "10.0.0.0/33", "fd00::/129", "nope/8", "10.0.0.0/8/1"])(
    "rejects %s",
    (cidr) => {
      expect(parseTrustedProxies(cidr)).toBeNull();
    },
  );

  it("accepts a list with blanks", () => {
    expect(parseTrustedProxies(" 10.0.0.0/8, ,fd00::/8 ")).not.toBeNull();
  });
});

describe("forwardedForHeaders without the preload", () => {
  it("forwards nothing: the chain header could be browser-supplied", () => {
    // Clear the marker explicitly: the preload may already be loaded in
    // this worker by the block below.
    const h = new Headers({ [PEER_CHAIN_HEADER]: "6.6.6.6" });
    const installed = Symbol.for("security-atlas.peer-chain");
    const g = globalThis as Record<symbol, unknown>;
    const prev = g[installed];
    g[installed] = undefined;
    try {
      expect(forwardedForHeaders(h, {})).toEqual({});
    } finally {
      g[installed] = prev;
    }
  });
});

describe("forwardedForHeaders over a real socket (peer-chain.cjs loaded)", () => {
  let server: http.Server;
  let port: number;
  let env: Record<string, string | undefined> = {};

  beforeAll(async () => {
    createRequire(import.meta.url)("../peer-chain.cjs");
    server = http.createServer((req, res) => {
      const h = new Headers();
      for (const [k, v] of Object.entries(req.headers)) {
        if (typeof v === "string") h.set(k, v);
      }
      res.end(JSON.stringify(forwardedForHeaders(h, env)));
    });
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    port = (server.address() as AddressInfo).port;
  });

  afterAll(async () => {
    await new Promise<void>((r) => server.close(() => r()));
  });

  async function call(headers: Record<string, string>) {
    const res = await fetch(`http://127.0.0.1:${port}/login`, { headers });
    return (await res.json()) as Record<string, string>;
  }

  it("ignores a forged X-Forwarded-For and a forged chain header from a direct browser", async () => {
    env = {};
    expect(
      await call({
        "X-Forwarded-For": "198.51.100.66",
        [PEER_CHAIN_HEADER]: "198.51.100.67",
      }),
    ).toEqual({ "X-Forwarded-For": "127.0.0.1" });
  });

  it("gives two clients behind a trusted proxy two different addresses", async () => {
    // The loopback peer stands in for the operator's reverse proxy.
    env = { WEB_TRUSTED_PROXY_CIDRS: "127.0.0.0/8" };
    const a = await call({ "X-Forwarded-For": "198.51.100.1" });
    const b = await call({ "X-Forwarded-For": "203.0.113.2" });
    expect(a).toEqual({ "X-Forwarded-For": "198.51.100.1" });
    expect(b).toEqual({ "X-Forwarded-For": "203.0.113.2" });
  });

  it("forwards nothing when WEB_TRUSTED_PROXY_CIDRS is malformed", async () => {
    env = { WEB_TRUSTED_PROXY_CIDRS: "not-a-cidr" };
    expect(await call({})).toEqual({});
  });
});
