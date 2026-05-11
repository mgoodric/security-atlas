import { cookies } from "next/headers"
import { NextResponse } from "next/server"

import { SESSION_COOKIE } from "@/lib/auth"
import { listAnchors } from "@/lib/api"

// Server-side proxy that injects the cookie's bearer token into the
// upstream call so the client never sees the token.
export async function GET() {
  const jar = await cookies()
  const bearer = jar.get(SESSION_COOKIE)?.value
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 })
  }
  try {
    const anchors = await listAnchors(bearer)
    return NextResponse.json({ anchors })
  } catch (err) {
    const e = err as { status?: number; message?: string }
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    )
  }
}
