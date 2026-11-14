// #2040 — record each request's real TCP peer before Next.js sees it.
//
// Loaded with `node --require ./peer-chain.cjs` ahead of the Next.js
// server (web.Dockerfile CMD, `npm run start`).
//
// Why a preload: Next.js 16 fills X-Forwarded-For with the socket peer
// only when the header is absent (base-server.js: `??=`). A browser that
// sends its own X-Forwarded-For therefore erases the one fact the BFF
// needs to tell a forged header from a trusted proxy's. App Router code
// never sees the socket, so the peer has to be captured here, at the
// http.Server, before any request handler runs.
//
// For every request this OVERWRITES PEER_CHAIN_HEADER with the chain a
// standards-following proxy would produce: the inbound X-Forwarded-For
// entries, then the socket peer. A browser-supplied copy of the header
// is discarded. lib/client-ip.ts walks the chain right-to-left against
// WEB_TRUSTED_PROXY_CIDRS, and only does so when PEER_CHAIN_INSTALLED is
// set on globalThis, i.e. when this file ran and the header is ours.
"use strict";

const http = require("node:http");
const https = require("node:https");

const PEER_CHAIN_HEADER = "x-atlas-peer-chain";
const PEER_CHAIN_INSTALLED = Symbol.for("security-atlas.peer-chain");

function peerChain(req) {
  const xff = req.headers["x-forwarded-for"];
  const inbound = Array.isArray(xff) ? xff.join(",") : xff ?? "";
  const hops = inbound
    .split(",")
    .map((h) => h.trim())
    .filter(Boolean);
  const peer = req.socket?.remoteAddress;
  if (peer) hops.push(peer);
  return hops.join(", ");
}

function wrapEmit(proto) {
  const emit = proto.emit;
  proto.emit = function (event, req, ...rest) {
    if (event === "request" && req && req.headers) {
      req.headers[PEER_CHAIN_HEADER] = peerChain(req);
    }
    return emit.call(this, event, req, ...rest);
  };
}

if (!globalThis[PEER_CHAIN_INSTALLED]) {
  wrapEmit(http.Server.prototype);
  if (https.Server.prototype.emit !== http.Server.prototype.emit) {
    wrapEmit(https.Server.prototype);
  }
  globalThis[PEER_CHAIN_INSTALLED] = true;
}

module.exports = { PEER_CHAIN_HEADER, PEER_CHAIN_INSTALLED, peerChain };
