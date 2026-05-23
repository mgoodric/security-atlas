import type { NextConfig } from "next";

// Slice 208 — Next.js rewrites for /v1/*, /health, /metrics.
//
// Why: post-slice-206, web/proxy.ts exempts /v1/* and /metrics from the
// redirect-to-login gate, so an unauthenticated request reaches Next.js
// proper. But Next.js itself has no route handler at /v1/me etc.; the
// catch-all returns 404. The dashboard's browser-side data fetches
// (e.g. fetch('/v1/me')) then fail with JSON parse errors on HTML.
//
// The rewrites below forward the three well-known path prefixes to the
// atlas Go backend at ATLAS_HTTP_URL. Same env var the BFF route
// handlers use server-side (web/lib/api.ts:apiBaseURL()), so deployment
// topology is uniform — docker-compose.yml already sets it; local dev
// gets the default.
//
// Auth contract (P0-A3): rewrites preserve cookies + headers verbatim.
// The atlas slice-190 jwtmw middleware continues to gate every /v1/*
// request and returns 401 on missing/invalid JWT. The Next.js layer
// does NOT inject auth, does NOT strip cookies, does NOT mediate. AC-5
// in the slice spec verifies this end-to-end: unauthenticated
// /v1/anchors returns 401 from atlas, not 404 from Next.js.
//
// Scope (P0-A1 + P0-A2): the rewrites only cover three prefixes:
//   * /v1/:path*  — atlas HTTP API (slice-190 jwtmw gates)
//   * /health     — atlas liveness (public by slice-052 contract)
//   * /metrics    — slice-121 OTel runtime metrics
// They do NOT cover /api/* (Next.js BFF route handlers — server-side
// credential handling stays Next.js-managed) NOR /oauth/* (Next.js
// OIDC callback — server-side cookie writing stays Next.js-managed).

// ATLAS_HTTP_URL is the operator-controlled backend origin. Default
// matches cmd/atlas/main.go's local bind so `next dev` outside
// docker-compose Just Works. Documented in
// docs/audit-log/208-nextjs-rewrites-decisions.md D1.
const ATLAS_HTTP_URL_DEFAULT = "http://localhost:8080";

function resolveAtlasHTTPURL(): string {
  const fromEnv = process.env.ATLAS_HTTP_URL;
  if (fromEnv && fromEnv.length > 0) {
    return fromEnv;
  }
  // D1: emit a one-shot warning so devs running `next dev` outside
  // docker-compose see the fallback. Production / docker-compose
  // deployments always set ATLAS_HTTP_URL so this path is silent there.
  console.warn(
    `[next.config] ATLAS_HTTP_URL not set; rewrites will forward to ${ATLAS_HTTP_URL_DEFAULT}. Set ATLAS_HTTP_URL to override.`,
  );
  return ATLAS_HTTP_URL_DEFAULT;
}

const nextConfig: NextConfig = {
  // Slice 037: emit a self-contained `.next/standalone` server bundle so
  // web.Dockerfile's runtime stage ships only the traced production
  // dependencies (a few MB) instead of the full node_modules tree. The
  // docker-compose self-host bundle runs `node server.js` from that
  // output.
  output: "standalone",

  // Slice 208 — see header comment.
  async rewrites() {
    const target = resolveAtlasHTTPURL();
    return [
      { source: "/v1/:path*", destination: `${target}/v1/:path*` },
      { source: "/health", destination: `${target}/health` },
      { source: "/metrics", destination: `${target}/metrics` },
    ];
  },
};

export default nextConfig;
