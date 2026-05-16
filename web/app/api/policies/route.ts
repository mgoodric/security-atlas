// Slice 101 — BFF proxy for `/policies` list view.
//
// Reads the bearer cookie server-side and calls `/v1/policies` upstream.
// The bearer never reaches the browser. Mirrors the slice 098 controls /
// slice 100 risks route pattern (cookie -> bearer -> upstream call). The
// row source is `internal/api/policies/handlers.go` (`policyWire`) per
// design doc `Plans/canvas/12-ui-fill-in-design-decisions.md` §7.
//
// Why a `/api/policies` route: per the BFF-per-page convention slices
// 098/099/100/101/102 follow, keeping the URL shape predictable. When
// the backend `?include=ack_rate` extension lands (spillover slice
// 107), this route gets the upgrade in one place — the page does not
// have to change.
//
// Per slice 101 P0-A4 the list is read-only: the BFF only exposes GET.
// Policy create / update / publish remain on the existing
// `/v1/policies/...` routes for whichever surface owns the CRUD flow.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listPolicies } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const policies = await listPolicies(bearer);
    return NextResponse.json({ policies });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
