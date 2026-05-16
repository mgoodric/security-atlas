// Slice 100 — BFF proxy for `/risks` list view (GET).
// Slice 105 — BFF proxy for `/risks/new` form submit (POST).
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
// Slice 100 originally shipped this as GET-only. Slice 105 adds POST so
// the `/risks/new` form can submit through the same per-page BFF pattern
// without inventing a second route. The POST handler forwards the JSON
// body verbatim to `/v1/risks` — the slice-019 backend write path is
// unchanged. Tenant isolation is enforced upstream by RLS (canvas
// invariant #6); the BFF only carries the bearer cookie.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL, listRisks } from "@/lib/api";
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

// POST /api/risks (slice 105) — forwards a risk-create payload to the
// slice-019 backend write path. The body shape mirrors `createReq` in
// `internal/api/risks/handlers.go` exactly — the BFF does not validate
// or reshape; the upstream is the single source of truth for field
// validation. Upstream status + body pass through verbatim so the form
// can surface inline 4xx messages without losing user input.
export async function POST(req: NextRequest): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/risks`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
