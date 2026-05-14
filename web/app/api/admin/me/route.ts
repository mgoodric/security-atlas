// Slice 060 — admin self-introspection BFF.
//
// The admin layout needs to know whether the current bearer is an admin
// credential (slice 034's `cred.IsAdmin` → returned on the credential list).
// Calling /v1/admin/credentials itself is the cheapest "am I admin?" test:
// non-admin gets 403, admin gets 200. We surface the boolean here so the
// admin layout can render a 403 page (AC-7) without leaking which sub-area
// exists.
//
// The route is intentionally narrow: { is_admin: boolean }. Tenant id and
// credential fingerprint stay on the platform; the BFF never echoes them.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ is_admin: false }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/admin/credentials`, {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  if (upstream.status === 200) {
    return NextResponse.json({ is_admin: true });
  }
  if (upstream.status === 403) {
    return NextResponse.json({ is_admin: false }, { status: 200 });
  }
  if (upstream.status === 401) {
    return NextResponse.json({ is_admin: false }, { status: 401 });
  }
  return NextResponse.json(
    { is_admin: false, error: `upstream ${upstream.status}` },
    { status: 502 },
  );
}
