// Slice 151 — BFF proxy for the tenant control-list endpoint.
//
// Reads the bearer cookie server-side and calls `GET /v1/controls` upstream
// (slice 151 backend handler). The bearer never reaches the browser.
//
// Why a `/api/controls-list` route when `/api/controls` already exists:
// the pre-existing `/api/controls` route (slice 098 + 104) proxies
// `/v1/anchors?include=state` — that surface returns the SCF catalog
// with state cells joined, NOT the tenant `controls` table the
// slice-151 risk-create form's multi-select needs. The two endpoints
// are semantically distinct (catalog vs tenant control list), so they
// get distinct BFF routes. Documented in PR D-151-3.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { fetchTenantControls } from "@/lib/api/controls-list";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const controls = await fetchTenantControls(bearer);
    return NextResponse.json({ controls, count: controls.length });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
