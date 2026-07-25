// Slice 688 — BFF for the vendor_reviews ledger. The bearer cookie stays
// httpOnly: the browser calls this route handler, which forwards to the
// RLS-scoped platform endpoints (GET/POST /v1/vendors/{id}/reviews). The
// page never passes a tenant_id; the cookie session is the only tenant
// context (invariant #6).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  listVendorReviews,
  recordVendorReview,
  VendorReviewWrite,
} from "@/lib/api/vendors";

export async function GET(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const reviews = await listVendorReviews(bearer, id);
    return NextResponse.json({ reviews });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: VendorReviewWrite;
  try {
    body = (await req.json()) as VendorReviewWrite;
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  try {
    const review = await recordVendorReview(bearer, id, body);
    return NextResponse.json({ review }, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
