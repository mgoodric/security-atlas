// Slice 135 — BFF for the audit-log data-export endpoint.
//
// AC-13. This route forwards the browser's
// `GET /api/audit-log/export?format=<csv|json|xlsx>&from=...&to=...&kind=...&actor=...`
// query string verbatim to the platform's
// `GET /v1/admin/audit-log/export?...` endpoint and STREAMS the
// response back. Critical posture:
//
//   - Streaming forward: the body is piped via `upstream.body` (a
//     ReadableStream) directly into the NextResponse, with NO
//     buffering. A 100-MB XLSX export should never materialise in
//     the BFF's memory (slice 135 P0-A7).
//
//   - Bearer auth: the same `SESSION_COOKIE` (post-slice-206:
//     `atlas_jwt`) the slice-125 unified-read BFF uses. Slice 110
//     P0-A2 narrow-scope rule — `atlas_session` cookie is NOT
//     forwarded; the audit-log export endpoint authenticates on the
//     bearer alone, same as the read.
//
//   - Header passthrough: Content-Type, Content-Disposition, and the
//     security headers the backend emits (X-Content-Type-Options) all
//     flow through verbatim. The backend is the authority on the
//     filename; the BFF cannot rename the attachment.
//
// All validation (90-day window, kind whitelist, row cap, frozen
// clamp, role gate) lives in the platform handler; the BFF is a
// pure passthrough that adds only the bearer.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

// Headers the BFF forwards to the browser from the upstream response.
// Content-Type + Content-Disposition are load-bearing (the browser
// uses them to trigger the file-save dialog). X-Content-Type-Options
// is the slice 135 nosniff guard the backend sets.
const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
];

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(request.url);
  const upstreamURL = `${apiBaseURL()}/v1/admin/audit-log/export${url.search}`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  // Error path: backend returned a JSON error body. The Content-Type
  // is application/json (not the export format). Pass it through so
  // the browser sees the upstream error message.
  if (!upstream.ok) {
    const text = await upstream.text();
    return new NextResponse(text, {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("Content-Type") ?? "application/json",
      },
    });
  }

  // Happy path: stream the body. fetch's `upstream.body` is a
  // ReadableStream; Next.js's NextResponse accepts that directly,
  // wiring upstream→client without buffering.
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
