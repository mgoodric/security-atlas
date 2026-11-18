// Slice 060 — POST /api/admin/credentials/{id}/revoke (proxy to slice 034).
//
// 204 No Content on success. The frontend re-fetches the list and the
// row disappears.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { revokeAdminCredential } from "@/lib/api/admin";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { SEGMENT, rejectUnsafeSegment } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  const bad = rejectUnsafeSegment({ id }, SEGMENT.credentialID);
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
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
