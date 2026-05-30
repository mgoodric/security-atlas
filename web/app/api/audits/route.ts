// Slice 102 — BFF proxy for `/audits` list view (GET).
// Slice 149 — adds POST so `/audits/new` form can submit through the
//             same per-page BFF pattern without inventing a second route.
//
// Reads the bearer cookie server-side and calls `/v1/audit-periods`
// upstream. The bearer never reaches the browser. Mirrors the slice
// 098 pattern (`web/app/api/controls/route.ts`) so the BFF shape stays
// predictable across the five list-view slices (098/099/100/101/102),
// and the slice 105 GET+POST shape (`web/app/api/risks/route.ts`).
//
// Why a `/api/audits` route instead of consuming the existing
// `/api/audit/periods` (slice 042) route:
//
//   - `/api/audit/periods` (slice 042) forwards `/v1/me/audit-periods`
//     which returns the CALLER's auditor assignments only. That is the
//     correct scoping for the per-control walk-through workspace.
//   - `/api/audits` (this slice) forwards `/v1/audit-periods` which
//     returns ALL audit periods the tenant has created. That is the
//     correct scoping for the security-leader's period index.
//
// Different endpoint, different scope, different consumer page. The
// two routes do NOT collide — `/api/audits` (plural list) vs
// `/api/audit/periods` (singular workspace-context list of one user's
// assignments).
//
// Tenant isolation (Invariant 6): the platform derives the tenant from
// the bearer; this BFF never reads or forwards a tenant_id from the
// client. RLS denies cross-tenant reads at the DB layer.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/audit-periods`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

// POST /api/audits (slice 149) — forwards an audit-period-create payload
// to the slice-028 backend write path `POST /v1/audit-periods`. The body
// shape mirrors `createReq` in `internal/api/auditperiods/handlers.go`
// exactly — the BFF does not validate or reshape; the upstream is the
// single source of truth for field validation. Upstream status + body
// pass through verbatim so the form can surface inline 4xx messages
// without losing user input.
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
  const upstream = await fetch(`${apiBaseURL()}/v1/audit-periods`, {
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
