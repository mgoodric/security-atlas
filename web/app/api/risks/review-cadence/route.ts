// #2059 — BFF proxy for `GET /v1/risks/review-cadence`.
//
// The organisation risk review cadence (days). The `/risks/new` form reads
// it to show the review date a new risk will get (created + cadence) and
// the settings Tenant tab reads it to show the current value. Upstream
// status + body pass through verbatim; the bearer never reaches the
// browser.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/risks/review-cadence`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
