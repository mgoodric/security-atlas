// #2108 — BFF proxy for `POST /v1/action-plans/{id}/verify`. Forwards the
// bearer cookie; the bearer never reaches the browser. The platform owns
// every refusal: 403 for a non-approver or the plan's owner, 409 for a
// plan with linked controls, 422 for a plan that is not completed.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/action-plans/${encodeURIComponent(id)}/verify`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
