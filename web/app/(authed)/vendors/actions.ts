// Client-side wrappers for the vendor mutation endpoints. They go through
// the Next.js route handlers so the bearer cookie stays httpOnly on the
// browser — same pattern as /api/anchors.

import { APIError, Vendor, VendorWrite } from "@/lib/api";

export async function createVendorFromCookieSession(
  body: VendorWrite,
): Promise<Vendor> {
  const res = await fetch("/api/vendors", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const decoded = (await res.json()) as { vendor: Vendor };
  return decoded.vendor;
}

export async function updateVendorFromCookieSession(
  id: string,
  body: VendorWrite,
): Promise<Vendor> {
  const res = await fetch(`/api/vendors/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const decoded = (await res.json()) as { vendor: Vendor };
  return decoded.vendor;
}
