// Slice 384 — BFF proxy for `/action-plans/[id]` (GET detail, PATCH update,
// DELETE soft-delete). Forwards the bearer cookie to upstream
// `/v1/action-plans/{id}`. The bearer never reaches the browser. RLS
// enforces tenant isolation (invariant 6); a cross-tenant id resolves to a
// clean upstream 404.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

async function proxy(
  req: NextRequest,
  id: string,
  method: "GET" | "PATCH" | "DELETE",
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const headers: Record<string, string> = {
    Authorization: `Bearer ${bearer}`,
  };
  let body: string | undefined;
  if (method === "PATCH") {
    body = await req.text();
    headers["Content-Type"] = "application/json";
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/action-plans/${encodeURIComponent(id)}`,
    { method, headers, body, cache: "no-store" },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

export async function GET(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  return proxy(req, id, "GET");
}

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  return proxy(req, id, "PATCH");
}

export async function DELETE(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  return proxy(req, id, "DELETE");
}
