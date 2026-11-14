// #2040 — the browser's client IP, for the BFF to forward to the API.
//
// The /login server action calls /auth/local/login server-to-server, so
// without this the API's clientIP() sees the web container for every UI
// login and #1913's per-IP bucket becomes one global ceiling.
//
// The address comes from the web server's OWN trusted chain, never from
// a browser-supplied X-Forwarded-For. web/peer-chain.cjs (a node
// --require preload) overwrites PEER_CHAIN_HEADER on every request with
// "<inbound X-Forwarded-For hops>, <socket peer>". This module walks that
// chain right-to-left the way the API's internal/api/auth/clientip.go
// does: a hop is accepted only while the address that delivered it is
// inside WEB_TRUSTED_PROXY_CIDRS, so a forged prefix is never reached.
//
// Fail closed: when the preload did not run (the header could then be
// browser-supplied), the chain is malformed, or WEB_TRUSTED_PROXY_CIDRS
// does not parse, the result is null and the BFF forwards no header. The
// API then keys on the web container, which is the pre-#2040 behaviour.

import { BlockList, isIP } from "node:net";

export const PEER_CHAIN_HEADER = "x-atlas-peer-chain";
const PEER_CHAIN_INSTALLED = Symbol.for("security-atlas.peer-chain");
const WEB_TRUSTED_PROXY_CIDRS_ENV = "WEB_TRUSTED_PROXY_CIDRS";

// Matches the API's maxForwardedHops (clientip.go): bounds the walk
// against a pathological header.
const MAX_FORWARDED_HOPS = 50;

// peerChainInstalled reports whether web/peer-chain.cjs ran in this
// process. Only then is PEER_CHAIN_HEADER guaranteed to be ours.
export function peerChainInstalled(): boolean {
  return (globalThis as Record<symbol, unknown>)[PEER_CHAIN_INSTALLED] === true;
}

// normalizeIP returns the canonical text of an IP literal, unwrapping an
// IPv4-mapped IPv6 address (the socket form of an IPv4 peer on a
// dual-stack listener), or null when s is not an IP.
function normalizeIP(s: string): string | null {
  const v = s.trim();
  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(v);
  if (mapped && isIP(mapped[1]) === 4) return mapped[1];
  return isIP(v) === 0 ? null : v;
}

function family(ip: string): "ipv4" | "ipv6" {
  return isIP(ip) === 4 ? "ipv4" : "ipv6";
}

// parseTrustedProxies parses a comma-separated CIDR list. Returns null
// when any entry is malformed, so a typo never silently trusts the wrong
// range.
export function parseTrustedProxies(raw: string | undefined): BlockList | null {
  const list = new BlockList();
  for (const token of (raw ?? "").split(",")) {
    const entry = token.trim();
    if (!entry) continue;
    const [addr, prefix, extra] = entry.split("/");
    const ip = normalizeIP(addr ?? "");
    const bits = Number(prefix);
    if (!ip || extra !== undefined || !/^\d{1,3}$/.test(prefix ?? "")) {
      return null;
    }
    const fam = family(ip);
    if (bits > (fam === "ipv4" ? 32 : 128)) return null;
    list.addSubnet(ip, bits, fam);
  }
  return list;
}

// resolveClientIP walks a peer chain ("hop, hop, ..., peer") right-to-left
// and returns the first address not delivered by a trusted proxy.
export function resolveClientIP(
  chain: string,
  trusted: BlockList,
): string | null {
  const hops = chain
    .split(",")
    .map((h) => h.trim())
    .filter(Boolean);
  if (hops.length === 0 || hops.length > MAX_FORWARDED_HOPS + 1) return null;

  let candidate: string | null = null;
  for (let i = hops.length - 1; i >= 0; i--) {
    const ip = normalizeIP(hops[i]);
    if (!ip) {
      // Malformed hop: return the closest address we can attribute.
      return candidate;
    }
    candidate = ip;
    if (!trusted.check(ip, family(ip))) return candidate;
  }
  return candidate;
}

// clientIPFromRequest returns the browser's client IP for the request
// whose headers are given, or null when it cannot be derived safely.
export function clientIPFromRequest(
  headers: Headers,
  env: Record<string, string | undefined> = process.env,
): string | null {
  if (!peerChainInstalled()) return null;
  const chain = headers.get(PEER_CHAIN_HEADER);
  if (!chain) return null;
  const trusted = parseTrustedProxies(env[WEB_TRUSTED_PROXY_CIDRS_ENV]);
  if (!trusted) {
    console.error(
      `${WEB_TRUSTED_PROXY_CIDRS_ENV} is malformed; not forwarding the client IP`,
    );
    return null;
  }
  return resolveClientIP(chain, trusted);
}

// forwardedForHeaders is the header bag the BFF adds to a server-to-server
// call so the API's per-IP decisions see the browser, not the BFF. It
// carries ONLY the resolved address, never the inbound header.
export function forwardedForHeaders(
  headers: Headers,
  env?: Record<string, string | undefined>,
): Record<string, string> {
  const ip = clientIPFromRequest(headers, env);
  return ip ? { "X-Forwarded-For": ip } : {};
}
