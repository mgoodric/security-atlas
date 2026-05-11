import { NextResponse, type NextRequest } from "next/server"

import { SESSION_COOKIE } from "@/lib/auth"

// Redirect unauthenticated traffic to /login. The login page itself is
// excluded so the user can land there to enter a token. Next.js 16
// renamed this convention from `middleware` to `proxy`.
export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl

  if (pathname.startsWith("/login") || pathname.startsWith("/_next")) {
    return NextResponse.next()
  }

  const token = request.cookies.get(SESSION_COOKIE)
  if (!token?.value) {
    const url = request.nextUrl.clone()
    url.pathname = "/login"
    url.searchParams.set("from", pathname)
    return NextResponse.redirect(url)
  }
  return NextResponse.next()
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
}
