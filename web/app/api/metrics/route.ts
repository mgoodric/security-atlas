// Slice 097 — BFF for GET /v1/metrics (catalog list).
//
// The catalog is platform-shared so the upstream is not RLS-scoped, but
// the BFF still requires a session cookie — the page is behind the
// (authed) layout and the bearer is what the platform's middleware
// uses to verify the caller is authenticated.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listMetrics } from "@/lib/api/metrics";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(request: Request) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(request.url);
  const level = url.searchParams.get("level") ?? undefined;
  const category = url.searchParams.get("category") ?? undefined;
  try {
    const metrics = await listMetrics(bearer, { level, category });
    return NextResponse.json({ metrics, count: metrics.length });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
