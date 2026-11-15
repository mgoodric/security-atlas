// #1817 — BFF proxy for `GET /v1/risks/categories`.
//
// The organisation's risk categories. The `/risks/new` picker reads the
// active ones; the register filter and the risk detail read them with
// `include_archived=true`, so an archived category still has its name and
// an "archived" mark. Only `include_archived` is forwarded; upstream
// status + body pass through verbatim, so a bad value is upstream's 400.
// The bearer never reaches the browser.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { forwardJSON, noStore } from "@/lib/api/bff";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// #1818 — POST adds a category (the Taxonomy settings page). The body is
// forwarded as-is; upstream validates it and refuses a caller without
// risk-taxonomy rights (403).
export async function POST(req: Request): Promise<Response> {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  return noStore(
    await forwardJSON("/v1/risks/categories", {
      method: "POST",
      jsonBody: body,
    }),
  );
}

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const includeArchived = new URL(req.url).searchParams.get("include_archived");
  const qs =
    includeArchived === null
      ? ""
      : `?include_archived=${encodeURIComponent(includeArchived)}`;
  const upstream = await fetch(`${apiBaseURL()}/v1/risks/categories${qs}`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
