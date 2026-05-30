// Slice 060 — feature-flag BFF (proxy to slice 059).
//
// GET /api/admin/features -> upstream GET /v1/admin/features
//
// The PATCH route lives in [key]/route.ts and proxies to the same upstream
// PATCH /v1/admin/features/{key} endpoint.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listFeatureFlags } from "@/lib/api/admin";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const items = await listFeatureFlags(bearer);
    return NextResponse.json({ items });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
