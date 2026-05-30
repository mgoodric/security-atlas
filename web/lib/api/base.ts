// API base URL helpers for the platform's HTTP endpoints. The bearer token
// lives in a cookie that the platform reads server-side; client-side
// fetches send the cookie via credentials: "include".
//
// Server-side (BFF route handlers, RSC fetches) and client-side (browser)
// run on different network paths and need different base URLs:
//
//   - SERVER: a Next.js API route handler in the `web` container reaches
//     atlas over the internal Docker network. Default `http://atlas:8080`
//     (the compose service name); override with `ATLAS_HTTP_URL`.
//
//   - CLIENT: the browser reaches atlas through whatever public URL fronts
//     the deployment. Default empty string = same-origin relative URLs,
//     which works for any reverse proxy that routes /v1, /health, and
//     /api under the same hostname as the web frontend (e.g. NPM with
//     custom locations). Override at build time with
//     `NEXT_PUBLIC_API_BASE_URL` when the API lives on a different origin.
//
// The published `web` image is therefore deployment-agnostic — the
// compose sets `ATLAS_HTTP_URL` per environment, and the browser uses
// same-origin URLs through the reverse proxy.
//
// Slice 370 — extracted from the former `web/lib/api.ts` god-file as the
// shared URL + error primitives. `apiBaseURL` and `APIError` are PUBLIC
// (the sibling BFF helpers import them); the barrel `web/lib/api.ts`
// re-exports both so existing `@/lib/api` importers resolve unchanged.

const SERVER_DEFAULT = "http://atlas:8080";
const CLIENT_DEFAULT = "";

export function apiBaseURL(): string {
  if (typeof window === "undefined") {
    return (
      process.env.ATLAS_HTTP_URL ||
      process.env.NEXT_PUBLIC_API_BASE_URL ||
      SERVER_DEFAULT
    );
  }
  return process.env.NEXT_PUBLIC_API_BASE_URL || CLIENT_DEFAULT;
}

export class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}
