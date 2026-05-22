// Slice 142 — super_admin management BFF (list + grant).
//
// GET  /api/admin/super-admins -> upstream GET  /v1/admin/super-admins
// POST /api/admin/super-admins -> upstream POST /v1/admin/super-admins
//
// The DELETE route lives in [user_id]/route.ts and proxies to
// DELETE /v1/admin/super-admins/{user_id}.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { grantSuperAdmin, listSuperAdmins } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const items = await listSuperAdmins(bearer);
    return NextResponse.json({ items });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function POST(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: { user_id?: string };
  try {
    body = (await req.json()) as { user_id?: string };
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (typeof body?.user_id !== "string" || body.user_id.trim() === "") {
    return NextResponse.json({ error: "user_id is required" }, { status: 400 });
  }
  try {
    const out = await grantSuperAdmin(bearer, body.user_id.trim());
    return NextResponse.json(out);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
