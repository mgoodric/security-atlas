// Slice 108 — BFF proxy for DELETE /v1/me/sessions/{id}.
// Slice 110 — additionally forwards the slice-034 `atlas_session` cookie
// so the platform handler knows whether the targeted session is the
// caller's current one (relevant for confirm-dialog UX).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { OIDC_SESSION_COOKIE, SESSION_COOKIE } from "@/lib/auth";
import { buildSessionsForwardHeaders } from "../_headers";

export async function DELETE(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const oidc = jar.get(OIDC_SESSION_COOKIE)?.value;
  const { id } = await ctx.params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/me/sessions/${encodeURIComponent(id)}`,
    {
      method: "DELETE",
      headers: buildSessionsForwardHeaders(bearer, oidc),
    },
  );
  if (upstream.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const text = await upstream.text();
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    return new NextResponse(text, { status: upstream.status });
  }
}
