// Slice 099 + 106 + 234 — BFF proxy for `/evidence` list view.
//
// Reads the bearer cookie server-side and calls
// `GET /v1/evidence[?control_id=&kind=&result=&source_actor_type=&source_actor_id=&scope_cell_id=&since=&until=&cursor=&limit=]`
// upstream. The bearer never reaches the browser. Mirrors the slice 098
// controls + slice 102 audits BFF pattern so the BFF shape stays
// predictable across the list-view slices (098/099/100/101/102).
//
// Tenant isolation (Invariant 6): the platform derives the tenant from
// the bearer; this BFF never reads or forwards a tenant_id from the
// client. RLS denies cross-tenant reads at the DB layer. The BFF also
// explicitly whitelists the query params it forwards — arbitrary
// caller-supplied keys (`tenant_id`, `debug`, etc.) are dropped.
//
// Slice 106 changes:
//   * `control_id` is no longer required. When absent, the upstream
//     returns the tenant-wide ledger window (RLS continues to scope).
//     The BFF therefore drops the required-control_id 400 guard.
//   * FORWARD_PARAMS gains `kind`, `result`, `source_actor_type`,
//     `source_actor_id` — the four new optional filter keys.
//
// Slice 234 changes:
//   * FORWARD_PARAMS gains `scope_cell_id` — the new Scope filter pill
//     binds to it. The upstream applies the SQL predicate against
//     `evidence_records.scope_id` (still RLS-scoped). The `since` param
//     was already in the whitelist (the Since pill reuses it).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

// FORWARD_PARAMS are the query keys the BFF is willing to forward to
// upstream. Anything else (e.g. `tenant_id`, `debug`) is dropped so a
// malicious or buggy caller cannot leak a privilege-escalation hint
// through this proxy.
const FORWARD_PARAMS = [
  "control_id",
  "kind",
  "result",
  "source_actor_type",
  "source_actor_id",
  // Slice 234 — Scope filter pill binds to `scope_cell_id`.
  "scope_cell_id",
  "since",
  "until",
  "cursor",
  "limit",
];

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(req.url);

  const out = new URLSearchParams();
  for (const key of FORWARD_PARAMS) {
    const v = url.searchParams.get(key);
    if (v) out.set(key, v);
  }

  const qs = out.toString();
  const upstream = await fetch(
    qs ? `${apiBaseURL()}/v1/evidence?${qs}` : `${apiBaseURL()}/v1/evidence`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
