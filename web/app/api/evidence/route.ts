// Slice 099 — BFF proxy for `/evidence` list view.
//
// Reads the bearer cookie server-side and calls
// `GET /v1/evidence?control_id=<uuid>[&since=&cursor=&limit=]` upstream.
// The bearer never reaches the browser. Mirrors the slice 098 controls
// + slice 102 audits BFF pattern so the BFF shape stays predictable
// across the list-view slices (098/099/100/101/102).
//
// Tenant isolation (Invariant 6): the platform derives the tenant from
// the bearer; this BFF never reads or forwards a tenant_id from the
// client. RLS denies cross-tenant reads at the DB layer. The BFF also
// explicitly whitelists the query params it forwards — arbitrary
// caller-supplied keys (`tenant_id`, `debug`, etc.) are dropped.
//
// Why this calls the existing `/v1/evidence?control_id=` shape (not a
// tenant-wide ledger endpoint):
//
//   The upstream `internal/api/controldetail/handler.go` Evidence
//   handler REQUIRES `control_id`. The slice text explicitly says
//   "preferred path is to extend the existing endpoint over adding a
//   new one" and "If the GET /v1/evidence?... endpoint shape needs an
//   extension, file as a backend follow-on slice rather than expanding
//   this PR." The v1 UI therefore selects a control via the filter pill
//   and reads its evidence ledger window. Spillover slice 106 files the
//   backend extension to make `control_id` optional + add `kind`/`result`
//   filter params for a true tenant-wide ledger view.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

// FORWARD_PARAMS are the query keys the BFF is willing to forward to
// upstream. Anything else (e.g. `tenant_id`, `debug`) is dropped so a
// malicious or buggy caller cannot leak a privilege-escalation hint
// through this proxy.
const FORWARD_PARAMS = ["control_id", "since", "until", "cursor", "limit"];

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(req.url);
  const controlID = url.searchParams.get("control_id");
  if (!controlID) {
    return NextResponse.json(
      { error: "control_id query parameter is required" },
      { status: 400 },
    );
  }

  const out = new URLSearchParams();
  for (const key of FORWARD_PARAMS) {
    const v = url.searchParams.get(key);
    if (v) out.set(key, v);
  }

  const upstream = await fetch(
    `${apiBaseURL()}/v1/evidence?${out.toString()}`,
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
