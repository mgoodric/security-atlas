// Slice 097 — BFF for GET /v1/metrics/{id}.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getMetric } from "@/lib/api/metrics";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  try {
    const detail = await getMetric(bearer, id);
    return NextResponse.json(detail);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
