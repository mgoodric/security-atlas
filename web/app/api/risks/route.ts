// Slice 100 — BFF proxy for `/risks` list view.
//
// Reads the bearer cookie server-side and calls `/v1/risks` upstream.
// The bearer never reaches the browser. Mirrors the slice 098 controls
// route pattern (cookie -> bearer -> upstream call). The row source is
// `internal/api/risks/handlers.go` (`riskWire`) per design doc
// `Plans/canvas/12-ui-fill-in-design-decisions.md` §7.
//
// Why a `/api/risks` route when `/api/dashboard/risks` already exists
// (slice 040): the dashboard route narrows to `treatment=mitigate` and
// is consumed by the "Top mitigate risks" panel — the list view needs
// the full unfiltered shape, and the BFF-per-page convention slices
// 098/099/100/101/102 follow keeps the URL shape predictable.
//
// Per slice 100 P0-A2 the list is read-only: the BFF only exposes GET.
// Risk-create / update / delete remain on the existing `/v1/risks/...`
// routes for whichever surface owns the CRUD flow.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listRisks } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const risks = await listRisks(bearer);
    return NextResponse.json({ risks });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
