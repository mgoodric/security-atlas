// Slice 278 — admin demo-teardown POST BFF.
//
// POST /api/admin/demo/teardown -> upstream POST /v1/admin/demo/teardown
//
// Forwards the bearer cookie + an EMPTY body. The upstream
// hard-codes the demo tenant slug. Per slice 205, the seeder's
// Teardown refuses to operate on a tenant that does not carry
// the demo_seed_apply forensic mark; this BFF surfaces upstream
// errors as their original status code.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { postAdminDemoTeardown } from "@/lib/api/admin";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const body = await postAdminDemoTeardown(bearer);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
