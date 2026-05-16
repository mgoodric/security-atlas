// Slice 108 — BFF proxy for DELETE /v1/me/sessions/{id}.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function DELETE(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/me/sessions/${encodeURIComponent(id)}`,
    {
      method: "DELETE",
      headers: { Authorization: `Bearer ${bearer}` },
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
