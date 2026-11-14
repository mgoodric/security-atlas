// #2040 — web vets X-Forwarded-For at the socket (web/peer-chain.cjs), and
// the login BFF forwards only that vetted value.
//
// The walk is unit-tested here through the preload's exports. The
// end-to-end proof (a forged header through the real standalone build, on
// the /v1 rewrite, a WebSocket upgrade and the login action) is
// web/scripts/check-forwarded-for.mjs, run in CI's prod-build job.

import { createRequire } from "node:module";

import { afterEach, describe, expect, it } from "vitest";

import { clientIPFromRequest, forwardedForHeaders } from "./client-ip";

type BlockList = import("node:net").BlockList;
type PeerChain = {
  PEER_CHAIN_INSTALLED: symbol;
  parseTrustedProxies(raw: string | undefined): BlockList;
  peerChain(req: {
    headers: Record<string, string | string[] | undefined>;
    socket?: { remoteAddress?: string };
  }): string[];
  resolveClientIP(hops: string[], trusted: BlockList): string | null;
  vetForwardedFor(
    req: {
      headers: Record<string, string | string[] | undefined>;
      socket?: { remoteAddress?: string };
    },
    trusted: BlockList,
  ): void;
};

// Loading the preload installs its http.Server hook in this worker, which
// is harmless here: no test in this file starts a server.
const pc = createRequire(import.meta.url)("../peer-chain.cjs") as PeerChain;

const trusted = (cidrs: string) => pc.parseTrustedProxies(cidrs);

function vetted(
  xff: string | string[] | undefined,
  peer: string | undefined,
  cidrs: string,
): string | undefined {
  const req = {
    headers: xff === undefined ? {} : { "x-forwarded-for": xff },
    socket: peer === undefined ? undefined : { remoteAddress: peer },
  };
  pc.vetForwardedFor(req, trusted(cidrs));
  return req.headers["x-forwarded-for"] as string | undefined;
}

describe("peer-chain.cjs vetForwardedFor", () => {
  it("replaces a forged header with the peer when no proxy is trusted", () => {
    expect(vetted("198.51.100.66", "192.0.2.7", "")).toBe("192.0.2.7");
  });

  it("gives two clients behind a trusted proxy two addresses", () => {
    expect(vetted("198.51.100.1", "10.0.0.5", "10.0.0.0/8")).toBe(
      "198.51.100.1",
    );
    expect(vetted("203.0.113.2", "10.0.0.5", "10.0.0.0/8")).toBe("203.0.113.2");
  });

  it("never reaches a forged prefix behind a trusted proxy", () => {
    expect(vetted("6.6.6.6, 198.51.100.1", "10.0.0.5", "10.0.0.0/8")).toBe(
      "198.51.100.1",
    );
  });

  it("unwraps an IPv4-mapped peer before matching", () => {
    expect(vetted("203.0.113.9", "::ffff:10.0.0.5", "10.0.0.0/8")).toBe(
      "203.0.113.9",
    );
  });

  it("matches IPv6 proxies", () => {
    expect(vetted("2001:db8::1", "fd00::2", "fd00::/8")).toBe("2001:db8::1");
  });

  it("deletes the header when there is no socket peer", () => {
    expect(vetted("198.51.100.66", undefined, "")).toBeUndefined();
  });

  it("stops at a malformed hop with the closest attributable address", () => {
    expect(vetted("nonsense", "10.0.0.5", "10.0.0.0/8")).toBe("10.0.0.5");
  });

  it("answers with an untrusted peer however many junk hops precede it", () => {
    const junk = Array.from(
      { length: 500 },
      (_, i) => `6.6.${i >> 8}.${i & 255}`,
    );
    expect(vetted(junk.join(", "), "192.0.2.7", "10.0.0.0/8")).toBe(
      "192.0.2.7",
    );
  });

  it("caps the trusted hops it steps over", () => {
    // 60 trusted hops between the client and web: the walk stops on a
    // trusted address after 50 and never reaches the forgeable far end.
    const chain = [
      "198.51.100.1",
      ...Array.from({ length: 60 }, () => "10.0.0.1"),
    ];
    expect(vetted(chain.join(", "), "10.0.0.2", "10.0.0.0/8")).toBe("10.0.0.1");
  });

  it("reads the array form of a repeated header in order", () => {
    expect(vetted(["6.6.6.6", "198.51.100.1"], "10.0.0.5", "10.0.0.0/8")).toBe(
      "198.51.100.1",
    );
    expect(vetted(["198.51.100.66"], "192.0.2.7", "")).toBe("192.0.2.7");
  });
});

describe("peer-chain.cjs parseTrustedProxies", () => {
  it.each(["10.0.0.0", "10.0.0.0/33", "fd00::/129", "nope/8", "10.0.0.0/8/1"])(
    "throws on %s",
    (cidr) => {
      expect(() => pc.parseTrustedProxies(cidr)).toThrow(
        /WEB_TRUSTED_PROXY_CIDRS/,
      );
    },
  );

  it("accepts a list with blanks", () => {
    expect(() =>
      pc.parseTrustedProxies(" 10.0.0.0/8, ,fd00::/8 "),
    ).not.toThrow();
  });
});

describe("client-ip forwardedForHeaders", () => {
  const g = globalThis as Record<symbol, unknown>;
  const prev = g[pc.PEER_CHAIN_INSTALLED];
  afterEach(() => {
    g[pc.PEER_CHAIN_INSTALLED] = prev;
  });

  it("forwards the vetted address when the preload is installed", () => {
    g[pc.PEER_CHAIN_INSTALLED] = true;
    const h = new Headers({ "x-forwarded-for": "198.51.100.1" });
    expect(forwardedForHeaders(h)).toEqual({
      "X-Forwarded-For": "198.51.100.1",
    });
  });

  it("forwards nothing without the preload: the header could be the browser's", () => {
    g[pc.PEER_CHAIN_INSTALLED] = undefined;
    const h = new Headers({ "x-forwarded-for": "198.51.100.66" });
    expect(forwardedForHeaders(h)).toEqual({});
  });

  it("forwards nothing when the header is not one address", () => {
    g[pc.PEER_CHAIN_INSTALLED] = true;
    expect(
      clientIPFromRequest(
        new Headers({ "x-forwarded-for": "1.2.3.4, 5.6.7.8" }),
      ),
    ).toBeNull();
    expect(clientIPFromRequest(new Headers())).toBeNull();
  });
});
