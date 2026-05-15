// Slice 073 — BFF route that proxies the elevated mark-first-signin
// call to the platform.
//
// Flow:
//   1. Browser POSTs /api/install/mark-first-signin (no body).
//   2. This route reads the session cookie (the bearer the user just
//      pasted on the login page).
//   3. If no bearer, return 401 — the user is not authenticated, the
//      flag must not be flipped on their behalf.
//   4. Otherwise forward to atlas's POST /v1/install/mark-first-signin
//      with `Authorization: Bearer <token>`.
//   5. Translate upstream 2xx -> 200, 401 -> 401, 503 -> 502, anything
//      else -> 502 with `error: "upstream <status>"`. The login flow
//      tolerates non-2xx responses (the marker write is best-effort —
//      the user is still signed in regardless), so 502 is non-fatal.
//
// Idempotent — the platform-side handler is a no-op when first_signin_at
// is already set, so re-calls after the marker is flipped return
// {marked: false, file_deleted: false}.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function POST() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json(
      { error: "unauthenticated" },
      { status: 401 },
    );
  }
  let upstream: Response;
  try {
    upstream = await fetch(`${apiBaseURL()}/v1/install/mark-first-signin`, {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
    });
  } catch (err) {
    const msg = err instanceof Error ? err.message : "upstream unreachable";
    return NextResponse.json(
      { error: `upstream fetch failed: ${msg}` },
      { status: 502 },
    );
  }
  if (upstream.status === 200) {
    let body: unknown = {};
    try {
      body = await upstream.json();
    } catch {
      // Empty body is fine — translate to {}.
    }
    return NextResponse.json(body, { status: 200 });
  }
  if (upstream.status === 401) {
    return NextResponse.json(
      { error: "unauthenticated" },
      { status: 401 },
    );
  }
  return NextResponse.json(
    { error: `upstream ${upstream.status}` },
    { status: 502 },
  );
}
