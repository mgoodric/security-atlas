// Slice 098 — BFF proxy for `/controls` list view.
//
// Reads the bearer cookie server-side and calls `/v1/anchors` upstream.
// The bearer never reaches the browser. Mirrors the slice 094 calendar
// route pattern (cookie → bearer → upstream call). The slice's row
// source is `internal/api/anchors/handlers.go` (`anchorWire`) per
// design doc `12-ui-fill-in-design-decisions.md` §7.
//
// Why a `/api/controls` route when `/api/anchors` already exists:
// 1. URL-shape parity with the page — `/controls` consumes `/api/controls`.
// 2. Forward-compatibility — when spillover slice 104 lands the
//    `?include=state` extension (or a dedicated `GET /v1/controls`
//    list endpoint), this route gets the upgrade in one place.
// 3. The 4 sibling list-view slices (099/100/101/102) follow the same
//    one-route-per-page convention so the BFF shape stays predictable.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listAnchors } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const anchors = await listAnchors(bearer);
    return NextResponse.json({ anchors });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
