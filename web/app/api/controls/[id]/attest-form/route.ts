import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getAttestForm } from "@/lib/api/attest";
import { rejectNonUUID } from "@/lib/api/bff";

// Slice 011 — server-side proxy for GET /v1/controls/{id}/attest-form.
// Reads the bearer cookie and forwards as Authorization on the upstream
// platform call. Pure read-only — no body shape to validate.

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const form = await getAttestForm(bearer, id);
    return NextResponse.json(form);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
