// OE-664 — BFF proxy for completing a personnel-security checklist item.
// Forwards the bearer cookie to upstream
// `/v1/personnel-security/checklist-items/{id}/complete` (OE-663), which
// writes the personnel_security.workflow.v1 evidence record and the
// completion columns in one transaction (invariant 9). Evidence +
// tracking only — no route on this surface provisions or deprovisions
// access anywhere (the OE-630 boundary).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  req: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const payload = await req.text();
  const upstream = await fetch(
    `${apiBaseURL()}/v1/personnel-security/checklist-items/${encodeURIComponent(
      id,
    )}/complete`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: payload,
      cache: "no-store",
    },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
