// Slice 097 — BFF for GET / PUT /v1/metrics/{id}/target.
//
// Tenant isolation: target rows are tenant-scoped; the upstream handler
// reads/writes through the tenant-bound atlas_app pool. The BFF passes
// the bearer through and lets the platform enforce the admin gate on
// PUT (slice-076 returns 403 if `cred.IsAdmin` is false).
//
// GET special case: upstream returns 404 when no target exists yet. The
// BFF translates that into a 200 with `null` body so the browser-side
// helper can render an empty-state without a try/catch dance.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import {
  getTarget,
  type MetricTargetUpsert,
  upsertTarget,
} from "@/lib/api/metrics";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  try {
    const target = await getTarget(bearer, id);
    return NextResponse.json(target);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function PUT(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  let body: MetricTargetUpsert;
  try {
    body = (await request.json()) as MetricTargetUpsert;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  try {
    const target = await upsertTarget(bearer, id, body);
    return NextResponse.json(target);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
