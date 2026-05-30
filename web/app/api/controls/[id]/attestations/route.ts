import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { submitAttestation, AttestSubmitRequest } from "@/lib/api/attest";

// Slice 011 — server-side proxy for POST /v1/controls/{id}/attestations.
// The body shape is forwarded verbatim so the upstream's schema
// validator is the single source of truth.

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  let body: AttestSubmitRequest;
  try {
    body = (await req.json()) as AttestSubmitRequest;
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  if (!body.statement || typeof body.statement !== "string") {
    return NextResponse.json(
      { error: "statement is required" },
      { status: 400 },
    );
  }
  try {
    const out = await submitAttestation(bearer, id, body);
    return NextResponse.json(out, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
