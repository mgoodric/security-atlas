// Slice 278 — admin demo-seed status BFF.
//
// GET /api/admin/demo/status -> upstream GET /v1/admin/demo/status
//
// The upstream returns {enabled: boolean} ONLY when the caller is
// admin-gated. Status is admin-gated upstream (slice 035 OPA admit);
// this BFF just forwards the bearer cookie. Non-admin callers
// receive 403 from the upstream and the BFF forwards that.
//
// IMPORTANT: the upstream Status route returns 200 regardless of the
// env var (the enabled flag IS the response); only /seed and /teardown
// refuse a disabled feature (409 feature_not_enabled, #2730). 5xx here
// means transient platform error.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getAdminDemoStatus } from "@/lib/api/admin";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const body = await getAdminDemoStatus(bearer);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
