import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";

// Slice 056 — shared forwarding helper for the hierarchical risk
// dashboard BFF routes. Each route reads the httpOnly bearer cookie,
// calls a typed client fn from `lib/api.ts` with it, and passes the
// result (or the upstream error status) back to the browser. The bearer
// never reaches the client. This is the slice 040 `dashboardProxy`
// shape, copied since the two route families are otherwise identical
// and live under different `/api` prefixes.

export async function hierarchyProxy<T>(
  load: (bearer: string) => Promise<T>,
): Promise<NextResponse> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    return NextResponse.json(await load(bearer));
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
