// Slice 072 — BFF route for the public /v1/version endpoint.
//
// Unlike every other BFF route under web/app/api, this one does NOT
// forward a bearer cookie. The upstream `/v1/version` is intentionally
// public (anti-criterion P0-A1) — it returns metadata about the binary,
// not tenant data. The route still proxies through the BFF so the
// browser hits a same-origin URL (no CORS dance) and so the response
// cache headers stay in this layer's control.
//
// Anti-criterion P0-A5 — over-fetching is the failure mode here. The
// browser-facing Cache-Control mirrors the upstream's 5-minute public
// cache; the TanStack Query hook on top of this route caches even
// longer (24h staleTime / 7d gcTime — see web/lib/version.ts).

import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";

export async function GET() {
  try {
    const upstream = await fetch(`${apiBaseURL()}/v1/version`, {
      // We intentionally bypass the fetch cache on the server side; the
      // browser-facing response below is what gets cached. Going through
      // the Next.js fetch cache here would pin a stale value across
      // binary restarts in a way the user can't easily invalidate.
      cache: "no-store",
    });
    const text = await upstream.text();
    return new NextResponse(text, {
      status: upstream.status,
      headers: {
        "Content-Type": "application/json",
        "Cache-Control": "public, max-age=300",
      },
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : "upstream error";
    return NextResponse.json(
      { error: "version_unavailable", detail: message },
      { status: 502 },
    );
  }
}
