// Slice 270 — BFF for the non-admin activity-ledger endpoint.
//
// AC-2. This route forwards the browser's `GET /api/activity?...` query
// string to the platform's `GET /v1/activity/unified?...` endpoint and
// pipes the response back. The platform handler
// (`internal/api/adminauditlog/activity.go`) owns ALL validation
// (90-day window guard, kind whitelist, cursor parsing) and ALL
// authorization (admin / auditor / grc_engineer / viewer /
// control_owner via the slice 156 `"activity"` OPA resource type).
//
// One BFF-specific transform: when the URL carries `?actor=me`, the
// BFF resolves the sentinel to the caller's user_id BEFORE forwarding.
// This keeps the client island free of an extra round-trip to /api/me
// and keeps the backend's `actor_filter` semantic literal-id-only
// (slice 270 D5).
//
// Resolution path: fetch /v1/me with the bearer; read `user_id` from
// the response; substitute into the upstream query string. If /v1/me
// fails, the sentinel passes through unchanged (the backend treats it
// as an exact literal "me" — which matches nothing — failing closed for
// the UI, not opening visibility).
//
// Slice 110 P0-A2 narrow-scope rule: the `atlas_session` cookie is
// forwarded ONLY on the /api/me/sessions* routes — broadening that
// surface here would leak the OIDC session id into a request path that
// does not need it. The activity endpoint authenticates on the bearer
// alone. We intentionally do NOT import `buildSessionsForwardHeaders`.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

const ACTOR_ME_SENTINEL = "me";

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  // Parse the incoming URL, resolve ?actor=me to the caller's user_id,
  // then forward to the platform.
  const url = new URL(request.url);
  const actorParam = url.searchParams.get("actor");
  if (actorParam === ACTOR_ME_SENTINEL) {
    const resolved = await resolveCallerUserID(bearer);
    if (resolved) {
      url.searchParams.set("actor", resolved);
    } else {
      // /v1/me unreachable — strip the `actor` filter rather than
      // leaving the literal "me" in place (which would silently match
      // nothing). The page renders the full visible activity for the
      // caller, which is the safer fail-open for a discovery surface.
      url.searchParams.delete("actor");
    }
  }

  const upstreamURL = `${apiBaseURL()}/v1/activity/unified${url.search}`;
  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  const text = await upstream.text();
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    // Non-JSON upstream body (rare) — pass through as-is so the user
    // can see what the platform returned rather than swallowing it.
    return new NextResponse(text, { status: upstream.status });
  }
}

// resolveCallerUserID fetches GET /v1/me and returns the response's
// `user_id` field, or null when /v1/me is unreachable or the field is
// missing. Used to expand the slice 270 D5 `?actor=me` sentinel.
async function resolveCallerUserID(bearer: string): Promise<string | null> {
  try {
    const meRes = await fetch(`${apiBaseURL()}/v1/me`, {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    });
    if (!meRes.ok) return null;
    const body = (await meRes.json()) as { user_id?: unknown };
    if (typeof body.user_id === "string" && body.user_id.length > 0) {
      return body.user_id;
    }
    return null;
  } catch {
    return null;
  }
}
