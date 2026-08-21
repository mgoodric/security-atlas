import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { APIError } from "@/lib/api/base";
import { forwardIncidentWrite, listIncidents } from "@/lib/api/incidents";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const bearer = await bearerFromCookie();
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    return NextResponse.json(await listIncidents(bearer));
  } catch (err) {
    return errorResponse(err);
  }
}

export async function POST(req: NextRequest): Promise<Response> {
  const bearer = await bearerFromCookie();
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const body = await jsonBody(req);
  const upstream = await forwardIncidentWrite(
    bearer,
    "/v1/incidents",
    "POST",
    body,
  );
  return passThrough(upstream);
}

async function bearerFromCookie(): Promise<string | undefined> {
  const jar = await cookies();
  return jar.get(ATLAS_JWT_COOKIE)?.value;
}

async function jsonBody(req: NextRequest): Promise<unknown> {
  try {
    return await req.json();
  } catch {
    return {};
  }
}

async function passThrough(upstream: Response): Promise<Response> {
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorResponse(err: unknown): Response {
  if (err instanceof APIError) {
    return NextResponse.json({ error: err.message }, { status: err.status });
  }
  const e = err as { status?: number; message?: string };
  return NextResponse.json(
    { error: e.message ?? "upstream error" },
    { status: e.status ?? 500 },
  );
}
