// #2040 — vet X-Forwarded-For at the socket, before Next.js sees it.
//
// Loaded with `node --require ./peer-chain.cjs` ahead of the Next.js
// server (web.Dockerfile CMD, `npm run start`).
//
// Why a preload: Next.js 16 fills X-Forwarded-For with the socket peer
// only when the header is absent (base-server.js: `??=`), and its
// rewrite proxy (/v1/*, /health, /metrics) forwards the header verbatim.
// A browser that sends its own X-Forwarded-For therefore both erases the
// socket peer and reaches atlas unchanged, which atlas trusts because it
// trusts web. App Router code never sees the socket, so the header has to
// be fixed here, at the http.Server, before any request handler runs.
//
// For every request this builds the chain a standards-following proxy
// would ("<inbound X-Forwarded-For hops>, <socket peer>"), walks it
// right-to-left against WEB_TRUSTED_PROXY_CIDRS the way atlas's
// internal/api/auth/clientip.go does, and REPLACES X-Forwarded-For with
// the single resolved client IP. When nothing can be resolved (no socket
// peer) the header is deleted. Every path through web, the login server
// action and the rewrites alike, then carries a vetted value or none.
//
// A malformed WEB_TRUSTED_PROXY_CIDRS throws at boot rather than trusting
// the wrong range per request.
"use strict";

const http = require("node:http");
const https = require("node:https");
const { BlockList, isIP } = require("node:net");

const PEER_CHAIN_INSTALLED = Symbol.for("security-atlas.peer-chain");
const WEB_TRUSTED_PROXY_CIDRS_ENV = "WEB_TRUSTED_PROXY_CIDRS";

// Matches atlas's maxForwardedHops (clientip.go): bounds the walk
// against a pathological header.
const MAX_FORWARDED_HOPS = 50;

// normalizeIP returns the canonical text of an IP literal, unwrapping an
// IPv4-mapped IPv6 address (the socket form of an IPv4 peer on a
// dual-stack listener), or null when s is not an IP.
function normalizeIP(s) {
  const v = String(s).trim();
  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(v);
  if (mapped && isIP(mapped[1]) === 4) return mapped[1];
  return isIP(v) === 0 ? null : v;
}

function family(ip) {
  return isIP(ip) === 4 ? "ipv4" : "ipv6";
}

// parseTrustedProxies parses a comma-separated CIDR list and throws on
// any malformed entry.
function parseTrustedProxies(raw) {
  const list = new BlockList();
  for (const token of String(raw ?? "").split(",")) {
    const entry = token.trim();
    if (!entry) continue;
    const [addr, prefix, extra] = entry.split("/");
    const ip = normalizeIP(addr ?? "");
    const bits = Number(prefix);
    const max = ip && family(ip) === "ipv4" ? 32 : 128;
    if (
      !ip ||
      extra !== undefined ||
      !/^\d{1,3}$/.test(prefix ?? "") ||
      bits > max
    ) {
      throw new Error(
        `${WEB_TRUSTED_PROXY_CIDRS_ENV}: invalid entry ${JSON.stringify(
          entry,
        )}`,
      );
    }
    list.addSubnet(ip, bits, family(ip));
  }
  return list;
}

// resolveClientIP walks "hop, hop, ..., peer" right-to-left and returns
// the first address not delivered by a trusted proxy, or null.
function resolveClientIP(hops, trusted) {
  if (hops.length === 0 || hops.length > MAX_FORWARDED_HOPS + 1) return null;
  let candidate = null;
  for (let i = hops.length - 1; i >= 0; i--) {
    const ip = normalizeIP(hops[i]);
    if (!ip) {
      // Malformed hop: the closest address we can attribute.
      return candidate;
    }
    candidate = ip;
    if (!trusted.check(ip, family(ip))) return candidate;
  }
  return candidate;
}

// peerChain is the hop list a proxy would build: the inbound
// X-Forwarded-For entries, then the socket peer. With no socket peer the
// chain is empty, so the request resolves to no client IP (fail closed).
function peerChain(req) {
  const peer = req.socket?.remoteAddress;
  if (!peer) return [];
  const xff = req.headers["x-forwarded-for"];
  const inbound = Array.isArray(xff) ? xff.join(",") : xff ?? "";
  const hops = inbound
    .split(",")
    .map((h) => h.trim())
    .filter(Boolean);
  hops.push(peer);
  return hops;
}

// vetForwardedFor replaces req's X-Forwarded-For with its resolved client
// IP, or deletes it when none resolves.
function vetForwardedFor(req, trusted) {
  const ip = resolveClientIP(peerChain(req), trusted);
  if (ip) {
    req.headers["x-forwarded-for"] = ip;
  } else {
    delete req.headers["x-forwarded-for"];
  }
}

function wrapEmit(proto, trusted) {
  const emit = proto.emit;
  proto.emit = function (event, req, ...rest) {
    if (event === "request" && req && req.headers) {
      vetForwardedFor(req, trusted);
    }
    return emit.call(this, event, req, ...rest);
  };
}

if (!globalThis[PEER_CHAIN_INSTALLED]) {
  const trusted = parseTrustedProxies(process.env[WEB_TRUSTED_PROXY_CIDRS_ENV]);
  wrapEmit(http.Server.prototype, trusted);
  if (https.Server.prototype.emit !== http.Server.prototype.emit) {
    wrapEmit(https.Server.prototype, trusted);
  }
  globalThis[PEER_CHAIN_INSTALLED] = true;
}

module.exports = {
  PEER_CHAIN_INSTALLED,
  normalizeIP,
  parseTrustedProxies,
  peerChain,
  resolveClientIP,
  vetForwardedFor,
};
