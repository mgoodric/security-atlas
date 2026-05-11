import { cookies } from "next/headers"
import { NextResponse } from "next/server"

import { SESSION_COOKIE } from "@/lib/auth"
import { getAnchorRequirements } from "@/lib/api"

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params
  const jar = await cookies()
  const bearer = jar.get(SESSION_COOKIE)?.value
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 })
  }
  try {
    const detail = await getAnchorRequirements(bearer, id)
    return NextResponse.json(detail)
  } catch (err) {
    const e = err as { status?: number; message?: string }
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    )
  }
}
