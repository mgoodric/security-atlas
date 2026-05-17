// Slice 097 — BFF for GET /v1/metrics/cascade.
//
// Forwards level + depth query params verbatim. Per the slice-076
// handler, depth defaults to 3 and is hard-capped at MaxCascadeDepth=6
// upstream — the BFF does no clamping of its own.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getCascade } from "@/lib/api/metrics";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(request: Request) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(request.url);
  const level = url.searchParams.get("level") ?? "board";
  const depthRaw = url.searchParams.get("depth");
  const depth = depthRaw ? Number(depthRaw) : undefined;
  try {
    const cascade = await getCascade(
      bearer,
      level,
      Number.isFinite(depth) ? depth : undefined,
    );
    return NextResponse.json(cascade);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
