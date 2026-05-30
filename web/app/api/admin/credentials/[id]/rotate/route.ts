// Slice 060 — POST /api/admin/credentials/{id}/rotate (proxy to slice 034).
//
// Rotate returns the successor's bearer_token plaintext + the predecessor's
// retirement deadline. Same write-once contract as Issue: the BFF passes
// the bearer through ONCE. The frontend shows it, the user copies it, and
// no part of the system retains it after the response leaves the browser.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { rotateAdminCredential } from "@/lib/api/admin";
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
    const rotated = await rotateAdminCredential(bearer, id);
    return NextResponse.json(rotated);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
