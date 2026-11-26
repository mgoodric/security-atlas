// #2172 — BFF proxy for a member's module access.
//
// PUT /api/admin/users/{id}/module-access -> upstream
// PUT /v1/admin/users/{id}/module-access, body {"modules": null | string[]}.
//
// Authority is enforced upstream (tenant admin; the member must belong to
// the caller's tenant). The BFF forwards the session cookie's bearer and
// passes the upstream status and body through, so a 400 (an admin
// narrowing themselves, an unknown module) reaches the dialog verbatim.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { rejectNonUUID, upstreamBody } from "@/lib/api/bff";

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export async function PUT(
  req: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  if (!UUID_PATTERN.test(id)) {
    return NextResponse.json({ error: "invalid user id" }, { status: 400 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/admin/users/${encodeURIComponent(id)}/module-access`,
    {
      method: "PUT",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: await req.text(),
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  try {
    return NextResponse.json(
      text ? JSON.parse(upstreamBody(upstream.status, text)) : null,
      {
        status: upstream.status,
      },
    );
  } catch {
    return new NextResponse(upstreamBody(upstream.status, text), {
      status: upstream.status,
    });
  }
}
