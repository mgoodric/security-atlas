// #2085 — BFF proxy for GET /v1/people, the directory <Person> resolves
// actor IDs against.
//
// Forwards the bearer cookie; the bearer never reaches the browser. The
// platform derives the tenant from the bearer and returns only ids,
// display names, statuses and key owners (no email, token or last4).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(request?: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  // #2174: ?module=<key> narrows the list to the members who can work in
  // that module (an assignee picker). The platform filters; this forwards
  // the one parameter and nothing else.
  const moduleKey = request
    ? new URL(request.url).searchParams.get("module")
    : null;
  const qs = moduleKey ? `?module=${encodeURIComponent(moduleKey)}` : "";
  const upstream = await fetch(`${apiBaseURL()}/v1/people${qs}`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
