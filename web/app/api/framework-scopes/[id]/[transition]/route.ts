import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { transitionFrameworkScope } from "@/lib/api/framework-scopes";
import { rejectNonUUID } from "@/lib/api/bff";

// Slice 018 — PATCH /v1/framework-scopes/{id}/{submit|approve|activate}.
// Transition name comes from the path segment; we whitelist the allowed
// values so a typo can't reach upstream as an unknown sub-resource.

const ALLOWED = new Set(["submit", "approve", "activate"] as const);

export async function PATCH(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; transition: string }> },
) {
  const { id, transition } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  if (!ALLOWED.has(transition as "submit" | "approve" | "activate")) {
    return NextResponse.json({ error: "unknown transition" }, { status: 404 });
  }
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: Record<string, unknown> | undefined;
  if (
    req.headers.get("content-length") &&
    req.headers.get("content-length") !== "0"
  ) {
    try {
      body = (await req.json()) as Record<string, unknown>;
    } catch {
      return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
    }
  }
  try {
    const framework_scope = await transitionFrameworkScope(
      bearer,
      id,
      transition as "submit" | "approve" | "activate",
      body,
    );
    return NextResponse.json({ framework_scope });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
