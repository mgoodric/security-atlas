// Slice 457 — BFF download route for the OSCAL signed-export bundle.
//
//   GET /api/audits/{id}/oscal-export
//       -> POST /v1/audit-periods/{id}/oscal-export:download
//
// This is the browser DOWNLOAD surface slice 423 deferred: the operator
// clicks an `<a href download>` (a native GET navigation), and this BFF
// forwards the bearer to the platform's slice-457 download verb, which
// returns the signed bundle as an attachment. The BFF passes the bytes
// + the Content-Disposition + Content-Type through verbatim so the
// browser raises a `download` event and drops a self-contained `.json`
// bundle (the four OSCAL members + the slice-413 signing manifest).
//
// Why GET-here -> POST-upstream:
//   * A native browser download is a GET navigation (`<a download href>`)
//     — there is no body and no fetch ceremony, so `waitForEvent
//     ("download")` fires (AC-3). The platform export verb is a POST
//     (it is a generate action, not a cacheable read). The BFF bridges
//     the two: the browser-friendly GET trigger maps to the upstream
//     POST. No request body is needed — the org/system SSP profile
//     fields are optional (the bridge defaults them), so the BFF posts
//     an empty body. A later slice can carry org-profile fields as query
//     params if the operator needs to override them.
//
// Tenant isolation (Invariant #6): the platform derives the tenant from
// the bearer and runs the export under the requesting tenant's
// `app.current_tenant`; a Tenant-B request for Tenant-A's period gets a
// 404. This BFF never reads or forwards a tenant_id from the client.
//
// Critical posture mirrors the slice-043 board-pack PDF BFF
// (`web/app/api/board-packs/[id]/pdf/route.ts`): read the upstream as an
// ArrayBuffer and stream the bytes back unchanged, preserving the
// attachment header. The bearer never reaches the browser; the
// `atlas_session` cookie is never forwarded (slice 110 narrow-scope
// rule).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const upstreamURL = `${apiBaseURL()}/v1/audit-periods/${encodeURIComponent(
    id,
  )}/oscal-export:download`;

  const upstream = await fetch(upstreamURL, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    // No body: org/system SSP-profile fields are optional and default in
    // the bridge. The handler accepts an empty body (Content-Length 0).
    body: "{}",
    cache: "no-store",
  });

  // Error path: the platform returned a JSON error body (404 unknown /
  // cross-tenant period, 409 not-frozen, 5xx). Pass the upstream status +
  // body through unchanged so the operator sees the upstream message.
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

  // Happy path: stream the bundle bytes back verbatim, preserving the
  // attachment disposition the platform set so the browser drops the
  // file with the platform's deterministic filename.
  const bytes = await upstream.arrayBuffer();
  const disposition =
    upstream.headers.get("Content-Disposition") ??
    `attachment; filename="oscal-bundle-${id}.json"`;
  return new NextResponse(bytes, {
    status: 200,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
      "Content-Disposition": disposition,
      "X-Content-Type-Options": "nosniff",
    },
  });
}
