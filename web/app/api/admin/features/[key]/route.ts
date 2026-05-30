// Slice 060 — PATCH /api/admin/features/{key} (proxy to slice 059).
//
// Anti-criterion P0: re-enabling a feature flag (false → true) is always a
// human click. The BFF does not auto-flip on package upgrade or any other
// implicit trigger. The PATCH body is forwarded verbatim — { enabled, reason }
// — and the upstream feature_flag_audit_log records actor + reason.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { patchFeatureFlag } from "@/lib/api/admin";
import { SESSION_COOKIE } from "@/lib/auth";

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ key: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { key } = await params;

  let body: { enabled: boolean; reason?: string };
  try {
    body = (await req.json()) as { enabled: boolean; reason?: string };
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (typeof body?.enabled !== "boolean") {
    return NextResponse.json(
      { error: "enabled must be a boolean" },
      { status: 400 },
    );
  }
  try {
    const out = await patchFeatureFlag(bearer, key, body);
    return NextResponse.json(out);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
