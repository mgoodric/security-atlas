// Slice 142 — super_admin demote BFF (DELETE).
//
// DELETE /api/admin/super-admins/{user_id}
//   -> upstream DELETE /v1/admin/super-admins/{user_id}
//
// Returns 204 on success. Returns 409 when the upstream rejects the
// demote as "last super_admin" (the load-bearing safety rail).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { demoteSuperAdmin } from "@/lib/api/admin";
import { SESSION_COOKIE } from "@/lib/auth";

export async function DELETE(
  _req: Request,
  { params }: { params: Promise<{ user_id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { user_id } = await params;
  try {
    await demoteSuperAdmin(bearer, user_id);
    return new NextResponse(null, { status: 204 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
