import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { forwardIncidentWrite } from "@/lib/api/incidents";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  const upstream = await forwardIncidentWrite(
    bearer,
    `/v1/incidents/${encodeURIComponent(id)}/close`,
    "POST",
    body,
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
