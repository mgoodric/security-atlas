// Slice 094 — BFF proxy for GET /v1/calendar.
//
// Reads the bearer cookie server-side and forwards to the platform with
// the request's query string preserved (from/to/types). The bearer
// never reaches the browser. Mirrors the dashboardProxy pattern from
// slice 040 but inlines the helper here because the calendar proxy
// also needs to thread through query params.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getCalendarEvents } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(request: Request) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(request.url);
  try {
    const body = await getCalendarEvents(bearer, {
      from: url.searchParams.get("from") ?? undefined,
      to: url.searchParams.get("to") ?? undefined,
      types: url.searchParams.get("types") ?? undefined,
    });
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
