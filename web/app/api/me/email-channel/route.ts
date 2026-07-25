// Slice 445 — BFF proxy for /v1/me/email-channel (GET + PUT).
//
// The per-user master email-channel opt-in toggle (AC-9). Identical proxy
// shape to /api/me/preferences — see that file for the rationale. Default
// is opted-OUT server-side (P0-445-7); this route carries the bearer and
// passes the {enabled} body through unchanged.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me/email-channel`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  return passthrough(upstream);
}

export async function PUT(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const body = await req.text();
  const upstream = await fetch(`${apiBaseURL()}/v1/me/email-channel`, {
    method: "PUT",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body,
  });
  return passthrough(upstream);
}

async function passthrough(upstream: Response): Promise<Response> {
  const text = await upstream.text();
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    return new NextResponse(text, { status: upstream.status });
  }
}
