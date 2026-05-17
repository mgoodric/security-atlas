// Slice 098 + 104 — BFF proxy for `/controls` list view.
//
// Reads the bearer cookie server-side and calls `/v1/anchors?include=state`
// upstream (slice 104 backend extension). The bearer never reaches the
// browser. Mirrors the slice 094 calendar route pattern
// (cookie → bearer → upstream call). The slice 104 join attaches a
// per-anchor `state` cell (or `null` when no tenant control is
// instantiated for that anchor).
//
// Why a `/api/controls` route when `/api/anchors` already exists:
// 1. URL-shape parity with the page — `/controls` consumes `/api/controls`.
// 2. Forward-compatibility — when a dedicated `GET /v1/controls` list
//    endpoint ships, this route gets the upgrade in one place.
// 3. The 4 sibling list-view slices (099/100/101/102) follow the same
//    one-route-per-page convention so the BFF shape stays predictable.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listAnchorsWithState } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const anchors = await listAnchorsWithState(bearer);
    return NextResponse.json({ anchors });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
