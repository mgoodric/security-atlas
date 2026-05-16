// Slice 097 — BFF for POST /v1/metrics/{id}/inputs (manual input).
//
// Tenant isolation: the upstream handler binds to the tenant-scoped
// atlas_app pool. The BFF forwards the bearer cookie verbatim and lets
// the platform enforce the admin role check (slice-076 returns 403 if
// `cred.IsAdmin` is false). The client side also gates the trigger
// button on `getSessionMe().is_admin` as defense-in-depth (decision D3).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createInput, type MetricInputCreate } from "@/lib/api/metrics";
import { SESSION_COOKIE } from "@/lib/auth";

export async function POST(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  let body: MetricInputCreate;
  try {
    body = (await request.json()) as MetricInputCreate;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  try {
    const input = await createInput(bearer, id, body);
    return NextResponse.json(input, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
