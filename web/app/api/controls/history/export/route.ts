// Slice 175 — BFF for the controls history-export endpoint.
//
// Forwards `GET /api/controls/history/export?format=<csv|json|xlsx>`
// to the platform's `GET /v1/controls/history/export?...` endpoint and
// STREAMS the response body back. Mirrors the slice 137 controls-export
// BFF exactly — same streaming posture, same passthrough headers, same
// bearer-only auth (no atlas_session cookie forwarded per slice 110
// P0-A2).
//
// Critical posture:
//
//   - Streaming forward: the body is piped via `upstream.body` (a
//     ReadableStream) directly into the NextResponse, with NO
//     buffering. A 500K-row history export should never materialise
//     in the BFF's memory; the slice 145 concurrency cap and the
//     slice 175 200 MB streaming-memory budget govern the backend.
//
//   - Bearer auth: the same `ATLAS_JWT_COOKIE` (post-slice-206:
//     `atlas_jwt`) the slice 100 `/api/controls` BFF uses. The
//     atlas_session cookie is NOT forwarded (slice 110 P0-A2).
//
//   - Header passthrough: Content-Type, Content-Disposition,
//     X-Content-Type-Options, Retry-After all flow through verbatim.
//
// All validation (format whitelist, row cap, role gate, concurrency
// cap) lives in the platform handler; the BFF is a pure passthrough
// that adds only the bearer.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// Headers the BFF forwards to the browser from the upstream response.
const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
  "retry-after",
];

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(request.url);
  const upstreamURL = `${apiBaseURL()}/v1/controls/history/export${url.search}`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  // Error path: backend returned a JSON error body.
  if (!upstream.ok) {
    const text = await upstream.text();
    const headers: Record<string, string> = {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
    };
    const ra = upstream.headers.get("Retry-After");
    if (ra) headers["Retry-After"] = ra;
    return new NextResponse(text, {
      status: upstream.status,
      headers,
    });
  }

  // Happy path: stream the body.
  const headers: Record<string, string> = {};
  for (const name of PASSTHROUGH_HEADERS) {
    const v = upstream.headers.get(name);
    if (v !== null) {
      headers[name] = v;
    }
  }

  return new NextResponse(upstream.body, {
    status: upstream.status,
    headers,
  });
}
