import { NextResponse, type NextRequest } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";

// Redirect unauthenticated traffic to /login. The login page itself is
// excluded so the user can land there to enter a token. Next.js 16
// renamed this convention from `middleware` to `proxy`.
//
// Slice 092: /api/version is the public version-metadata BFF route
// (web/app/api/version/route.ts — comment there documents the
// intentional-public contract). The proxy used to redirect it to
// /login, so the deployed UI's version-footer showed `v(none)`. The
// match below is exact-equality, NOT a prefix or regex, because P0-A1
// of slice 092 forbids broadening exemptions: a `startsWith("/api/v")`
// would silently expose `/api/vendors`, `/api/audit/period`, etc. The
// equality form fails closed if a sub-route is ever added.
export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;

  if (
    pathname.startsWith("/login") ||
    pathname.startsWith("/_next") ||
    pathname === "/api/version"
  ) {
    return NextResponse.next();
  }

  const token = request.cookies.get(SESSION_COOKIE);
  if (!token?.value) {
    const url = request.nextUrl.clone();
    url.pathname = "/login";
    url.searchParams.set("from", pathname);
    return NextResponse.redirect(url);
  }
  return NextResponse.next();
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
