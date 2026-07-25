import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  deleteVendor,
  getVendor,
  updateVendor,
  VendorWrite,
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
    const vendor = await getVendor(bearer, id);
    return NextResponse.json({ vendor });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: VendorWrite;
  try {
    body = (await req.json()) as VendorWrite;
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  try {
    const vendor = await updateVendor(bearer, id, body);
    return NextResponse.json({ vendor });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

// DELETE removes the vendor and (via the platform's CASCADE on
// vendor_scope_cells) its cell bindings — the affordance the edit-page
// copy promises (ATLAS-031, slice 679). The bearer cookie stays
// httpOnly: the browser calls this route handler, which forwards to the
// RLS-scoped platform endpoint. A successful delete returns 204.
export async function DELETE(
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
    await deleteVendor(bearer, id);
    return new NextResponse(null, { status: 204 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
