// Slice 137 — BFF for the controls UCF graph data-export endpoint.
//
// Forwards `GET /api/controls/export?format=<csv|json|xlsx>` to the
// platform's `GET /v1/controls/export?...` endpoint and STREAMS the
// response body back. Mirrors the slice 135 audit-log + slice 136
// risk-register export BFFs exactly — same streaming posture, same
// passthrough headers, same bearer-only auth (no atlas_session cookie
// forwarded per slice 110 P0-A2).
//
// Critical posture:
//
//   - Streaming forward: the body is piped via `upstream.body` (a
//     ReadableStream) directly into the NextResponse, with NO
//     buffering. A 500K-row controls export should never materialise
//     in the BFF's memory; the slice 145 concurrency cap and the
//     slice 137 200 MB streaming-memory budget govern the backend
//     side.
//
//   - Bearer auth: the same `SESSION_COOKIE` (post-slice-206:
//     `atlas_jwt`) the slice 100 `/api/controls` BFF uses. The
//     atlas_session cookie is NOT forwarded (slice 110 P0-A2) — the
//     export endpoint authenticates on the bearer alone.
//
//   - Header passthrough: Content-Type, Content-Disposition,
//     X-Content-Type-Options, Retry-After all flow through verbatim.
//     The backend authors the filename; the BFF cannot rename the
//     attachment. Retry-After is load-bearing on the 429 path
//     (slice 145 concurrency-cap denial).
//
// All validation (format whitelist, row cap, role gate, concurrency
// cap) lives in the platform handler; the BFF is a pure passthrough
// that adds only the bearer.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

// Headers the BFF forwards to the browser from the upstream response.
// Content-Type + Content-Disposition are load-bearing (the browser
// uses them to trigger the file-save dialog). X-Content-Type-Options
// is the slice 135 nosniff guard the backend sets. Retry-After is
// the slice 145 concurrency-cap 429 signal.
const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
  "retry-after",
];

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(request.url);
  const upstreamURL = `${apiBaseURL()}/v1/controls/export${url.search}`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  // Error path: backend returned a JSON error body. The Content-Type
  // is application/json (not the export format). Pass it through so
  // the browser sees the upstream error message — and the
  // Retry-After header (slice 145) for the 429 case.
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

  // Happy path: stream the body. fetch's `upstream.body` is a
  // ReadableStream; Next.js's NextResponse accepts that directly,
  // wiring upstream → client without buffering.
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
