// Slice 097 — BFF for GET /v1/metrics/{id}/observations.
//
// Tenant isolation: the upstream /observations handler reads through the
// tenant-bound atlas_app pool with the credential's tenant GUC set. The
// BFF passes the bearer cookie through without filtering — every read is
// tenant-scoped by the platform.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listObservations } from "@/lib/api/metrics";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  const url = new URL(request.url);
  const since = url.searchParams.get("since") ?? undefined;
  const until = url.searchParams.get("until") ?? undefined;
  const limitRaw = url.searchParams.get("limit");
  const limit = limitRaw ? Number(limitRaw) : undefined;
  try {
    const page = await listObservations(bearer, id, {
      since,
      until,
      limit: Number.isFinite(limit) ? limit : undefined,
    });
    return NextResponse.json(page);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
