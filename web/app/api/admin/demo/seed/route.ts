// Slice 278 — admin demo-seed POST BFF.
//
// POST /api/admin/demo/seed -> upstream POST /v1/admin/demo/seed
//
// Forwards the bearer cookie + an EMPTY body (P0-278-2: no user
// input flows through). The upstream hard-codes slug + scale.
//
// 409 {code: "feature_not_enabled"} from upstream means the env-var
// gate is unset (#2730). The frontend renders a "demo tools not
// enabled" banner from the status probe; the confirmation dialog flow
// only fires this endpoint when the probe reported enabled=true. A race
// where status flipped false between probe and click surfaces the 409
// here, and the page shows its message in the error Alert.

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
