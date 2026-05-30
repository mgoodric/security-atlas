// Slice 224 — BFF proxy for the tenant's scope cell list.
//
// The /controls page's Scope filter pill (slice 224) consumes this
// route to populate the dropdown options. The bearer cookie is read
// server-side and forwarded to the upstream `/v1/scopes/cells`
// endpoint (slice 017). The bearer never reaches the browser.
//
// Why a /api/scope-cells route when /api/scopes/* might be more
// natural: parity with the sibling list-view BFF routes
// (/api/controls, /api/risks, etc.) — one tiny route per shaped
// dropdown. Forward-compatible: if a richer scope-cells API ships
// later (filters, search, pagination), this route's params expand.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listScopeCells } from "@/lib/api/controls-list";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const cells = await listScopeCells(bearer);
    return NextResponse.json({ cells });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
