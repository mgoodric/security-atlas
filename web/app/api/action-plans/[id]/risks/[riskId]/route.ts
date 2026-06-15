// Slice 384 — BFF proxy for action-plan ↔ risk linkage (POST link, DELETE
// unlink). Forwards to upstream
// `/v1/action-plans/{id}/risks/{risk_id}`. The bearer never reaches the
// browser. Cross-tenant targets resolve to a clean upstream 404 (P0-384-4).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

async function proxy(
  id: string,
  riskId: string,
  method: "POST" | "DELETE",
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/action-plans/${encodeURIComponent(
      id,
    )}/risks/${encodeURIComponent(riskId)}`,
    {
      method,
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

export async function POST(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string; riskId: string }> },
): Promise<Response> {
  const { id, riskId } = await params;
  return proxy(id, riskId, "POST");
}

export async function DELETE(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string; riskId: string }> },
): Promise<Response> {
  const { id, riskId } = await params;
  return proxy(id, riskId, "DELETE");
}
