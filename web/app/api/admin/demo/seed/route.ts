// Slice 278 — admin demo-seed POST BFF.
//
// POST /api/admin/demo/seed -> upstream POST /v1/admin/demo/seed
//
// Forwards the bearer cookie + an EMPTY body (P0-278-2: no user
// input flows through). The upstream hard-codes slug + scale.
//
// 503 from upstream means env-var gate is unset. The frontend
// renders a "demo tools not enabled" banner in that case; the
// confirmation dialog flow only fires this endpoint when the
// status probe reported enabled=true. A race where status flipped
// false between probe and click would surface a 503 here; the
// page renders that as a destructive toast.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { postAdminDemoSeed } from "@/lib/api/admin";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const body = await postAdminDemoSeed(bearer);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
