// #2136 — a framework version's posture target.
//
//   GET -> GET /v1/framework-scopes/posture-targets/{framework_version_id}
//   PUT -> PUT /v1/framework-scopes/posture-targets/{framework_version_id}
//
// The platform owns every refusal: 403 for a role other than admin or
// grc_engineer, 400 for thresholds out of order or out of range, 404 for a
// version the tenant cannot see, 409 for a version with no activated
// scope. This BFF checks the id's shape and that the body is a JSON object,
// and passes the upstream status through.

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";
import { NextResponse } from "next/server";

function upstreamPath(versionId: string): string {
  return `/v1/framework-scopes/posture-targets/${encodeURIComponent(
    versionId,
  )}`;
}

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ versionId: string }> },
) {
  const { versionId } = await ctx.params;
  const bad = rejectNonUUID({ framework_version_id: versionId });
  if (bad) return bad;
  return forwardJSON(upstreamPath(versionId));
}

export async function PUT(
  req: Request,
  ctx: { params: Promise<{ versionId: string }> },
) {
  const { versionId } = await ctx.params;
  const bad = rejectNonUUID({ framework_version_id: versionId });
  if (bad) return bad;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return NextResponse.json(
      { error: "body must be a JSON object" },
      { status: 400 },
    );
  }
  // Forward the four target fields only; the platform validates them.
  const b = body as Record<string, unknown>;
  return forwardJSON(upstreamPath(versionId), {
    method: "PUT",
    jsonBody: {
      target_value: b.target_value,
      warning_threshold: b.warning_threshold,
      critical_threshold: b.critical_threshold,
      direction: b.direction,
    },
  });
}
