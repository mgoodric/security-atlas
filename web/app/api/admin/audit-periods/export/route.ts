// Slice 139 — BFF for the audit-periods data-export endpoint.
//
// Forwards `GET /api/admin/audit-periods/export?format=<csv|json|xlsx>`
// to the platform `GET /v1/admin/audit-periods/export?...` and STREAMS
// the response back. Critical posture matches slice 135's audit-log
// BFF (`web/app/api/audit-log/export/route.ts`):
//
//   - Streaming forward: `upstream.body` (a ReadableStream) is piped
//     directly into NextResponse with NO buffering. Even a 50K-row
//     XLSX must not materialise in BFF memory.
//   - Bearer auth: same `SESSION_COOKIE` (post-slice-206: `atlas_jwt`)
//     the slice-110 admin BFFs use. The `atlas_session` cookie is
//     NEVER forwarded (slice 110 P0-A2 narrow-scope rule).
//   - Header passthrough: Content-Type + Content-Disposition +
//     X-Content-Type-Options. The backend is the authority on the
//     filename.
//
// All validation (format, row cap, role gate, concurrency cap) lives
// in the platform handler; the BFF is a pure passthrough that adds
// only the bearer.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

// Headers forwarded from upstream response to browser. Content-Type +
// Content-Disposition are load-bearing for the file-save dialog;
// X-Content-Type-Options is the nosniff guard the backend sets.
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
  const upstreamURL = `${apiBaseURL()}/v1/admin/audit-periods/export${
    url.search
  }`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  // Error path: backend returned a JSON error body. Pass the upstream
  // status + body through unchanged so the operator sees the upstream
  // message.
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
  // wiring upstream -> client without buffering.
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
