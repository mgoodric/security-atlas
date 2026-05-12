import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { getAttestForm } from "@/lib/api";

// Slice 011 — server-side proxy for GET /v1/controls/{id}/attest-form.
// Reads the bearer cookie and forwards as Authorization on the upstream
// platform call. Pure read-only — no body shape to validate.

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
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
