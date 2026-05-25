// Slice 263 — BFF proxy for `/v1/questionnaires` (list + create).
//
// Reads the bearer cookie server-side and calls the slice 155 backend
// at /v1/questionnaires. The bearer never reaches the browser.
// Mirrors the slice 102 audits BFF shape (web/app/api/audits/route.ts)
// — thin proxy, upstream owns validation, errors pass through verbatim.
//
// Wire shape:
//   GET  /api/questionnaires            -> { questionnaires: Questionnaire[] }
//   POST /api/questionnaires { name }   -> Questionnaire
//
// Constitutional invariants:
//   * Invariant 6 (tenant isolation): the bearer is forwarded; the
//     platform's handlers run under the tenant GUC via
//     tenancy.ApplyTenant. The BFF never reads or forwards a
//     tenant_id (P0-263-1 — RLS bypass is impossible at this layer).
//   * P0-263-4: NO backend changes. This BFF consumes the slice 155
//     handler at internal/api/questionnaires/handlers.go (Create + List).

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/questionnaires`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

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
  const upstream = await fetch(`${apiBaseURL()}/v1/questionnaires`, {
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
