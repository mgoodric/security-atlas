// Slice 060 — POST /api/admin/credentials/{id}/revoke (proxy to slice 034).
//
// 204 No Content on success. The frontend re-fetches the list and the
// row disappears.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { revokeAdminCredential } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  try {
    await revokeAdminCredential(bearer, id);
    return new NextResponse(null, { status: 204 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
