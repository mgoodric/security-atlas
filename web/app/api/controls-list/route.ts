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

//
// Issue #1741 — `?include=state` switches to the paged
// `GET /v1/controls?include=state` read the /controls page lists by
// default; `scope`, `limit` and `offset` pass through. Without it the
// flat slice-151 envelope is unchanged.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import {
  fetchTenantControls,
  fetchTenantControlsWithState,
} from "@/lib/api/controls-list";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// parseNonNegativeInt returns undefined for an absent or malformed value
// so the platform applies its own default.
function parseNonNegativeInt(v: string | null): number | undefined {
  if (!v || !/^\d+$/.test(v)) return undefined;
  return Number.parseInt(v, 10);
}

export async function GET(req?: NextRequest): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const params = req?.nextUrl.searchParams;
  const include = params?.get("include") ?? "";
  if (include.split(",").some((p) => p.trim() === "state")) {
    try {
      const page = await fetchTenantControlsWithState(bearer, {
        scope: params?.get("scope") || undefined,
        limit: parseNonNegativeInt(params?.get("limit") ?? null),
        offset: parseNonNegativeInt(params?.get("offset") ?? null),
      });
      return NextResponse.json(page);
    } catch (err) {
      const e = err as { status?: number; message?: string };
      return NextResponse.json(
        { error: e.message ?? "upstream error" },
        { status: e.status ?? 500 },
      );
    }
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
