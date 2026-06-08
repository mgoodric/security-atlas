// Slice 584 — BFF proxy for /v1/me/slack-channel (GET + PUT).
//
// The per-user master Slack-channel opt-in toggle. Mirrors the slice-445
// /api/me/email-channel proxy shape exactly: carry the session-cookie
// bearer server-side and pass the {enabled} body through unchanged.
// Default is opted-OUT server-side (P0-543-3). The Slack target is
// OPERATOR-configured env, never user-supplied (P0-543-2 / SSRF) — this
// route only ever flips the caller's own boolean opt-in.

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
  const upstream = await fetch(`${apiBaseURL()}/v1/me/slack-channel`, {
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
  const upstream = await fetch(`${apiBaseURL()}/v1/me/slack-channel`, {
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
