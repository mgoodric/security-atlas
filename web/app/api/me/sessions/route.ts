// Slice 108 — BFF proxy for /v1/me/sessions (GET list + DELETE all-others).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me/sessions`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  return passthrough(upstream);
}

export async function DELETE(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me/sessions`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${bearer}` },
  });
  return passthrough(upstream);
}

async function passthrough(upstream: Response): Promise<Response> {
  const text = await upstream.text();
  if (upstream.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    return new NextResponse(text, { status: upstream.status });
  }
}
