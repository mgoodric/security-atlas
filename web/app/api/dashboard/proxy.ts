import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";

// Slice 040 — shared forwarding helper for the program dashboard BFF
// routes. Every dashboard route does the same three things: read the
// httpOnly bearer cookie, call a typed client fn from `lib/api.ts` with
// it, and pass the result (or the upstream error status) back to the
// browser. The bearer never reaches the client. This mirrors the
// slice-041 control-cluster BFF shape, collapsed to one helper since
// the four dashboard routes are otherwise identical.

export async function dashboardProxy<T>(
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
