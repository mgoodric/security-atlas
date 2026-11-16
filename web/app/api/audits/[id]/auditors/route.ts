// #2118 — /audits/[id] BFF: the period's auditors.
//
//   GET  -> GET  /v1/audit-periods/{id}/auditors   (assigned + candidates)
//   POST -> POST /v1/audit-periods/{id}/auditors   {user_id}
//
// The platform owns every refusal: 403 for a non-admin, 400 for a user who
// is not an active auditor of the tenant, 404 for a period outside it. This
// BFF checks the shape of the ids and passes the upstream status through.

import { forwardJSON, isUUID, rejectNonUUID } from "@/lib/api/bff";
import { NextResponse } from "next/server";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/audit-periods/${encodeURIComponent(id)}/auditors`);
}

export async function POST(
  req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  let body: { user_id?: unknown };
  try {
    body = (await req.json()) as { user_id?: unknown };
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  const userID = typeof body.user_id === "string" ? body.user_id : "";
  if (!isUUID(userID)) {
    return NextResponse.json(
      { error: "user_id must be a UUID" },
      { status: 400 },
    );
  }
  return forwardJSON(`/v1/audit-periods/${encodeURIComponent(id)}/auditors`, {
    method: "POST",
    jsonBody: { user_id: userID },
  });
}
