// #2040 — the browser's client IP, for the BFF to forward to the API.
//
// The /login server action calls /auth/local/login server-to-server, so
// without this the API's clientIP() sees the web container for every UI
// login and #1913's per-IP bucket becomes one global ceiling.
//
// The address is NOT taken from the browser. web/peer-chain.cjs (a node
// --require preload) replaces X-Forwarded-For on every request, before
// Next.js runs, with the single client IP it resolved from the socket peer
// and WEB_TRUSTED_PROXY_CIDRS, or deletes it. So once the preload is
// installed, the header this module reads is web's own.
//
// Fail closed: without the preload (e.g. `next dev`) the header could be
// browser-supplied, so the result is null and the BFF forwards nothing.
// The API then keys on the web container, the pre-#2040 behaviour.

import { isIP } from "node:net";

const PEER_CHAIN_INSTALLED = Symbol.for("security-atlas.peer-chain");

// peerChainInstalled reports whether web/peer-chain.cjs ran in this
// process. Only then is X-Forwarded-For guaranteed to be vetted.
export function peerChainInstalled(): boolean {
  return (globalThis as Record<symbol, unknown>)[PEER_CHAIN_INSTALLED] === true;
}

// clientIPFromRequest returns the browser's client IP for the request
// whose headers are given, or null when it cannot be derived safely.
export function clientIPFromRequest(headers: Headers): string | null {
  if (!peerChainInstalled()) return null;
  const xff = headers.get("x-forwarded-for")?.trim() ?? "";
  // The preload writes exactly one address; anything else is not ours.
  return isIP(xff) === 0 ? null : xff;
}

// forwardedForHeaders is the header bag the BFF adds to a server-to-server
// call so the API's per-IP decisions see the browser, not the BFF.
export function forwardedForHeaders(headers: Headers): Record<string, string> {
  const ip = clientIPFromRequest(headers);
  return ip ? { "X-Forwarded-For": ip } : {};
}
