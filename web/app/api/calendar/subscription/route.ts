// Slice 094 — BFF proxy for POST /v1/calendar/subscription.
//
// Mints (re-issues) a per-user ICS URL token. Reads the bearer cookie
// server-side; the response contains the freshly-minted opaque token
// embedded in a URL. The platform stores only the hash; this is the
// one chance the operator gets to copy the URL.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { postCalendarSubscription } from "@/lib/api/calendar";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const body = await postCalendarSubscription(bearer);
    return NextResponse.json(body, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
