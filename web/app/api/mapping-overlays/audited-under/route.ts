// #1802 — BFF for GET /v1/mapping-overlays/audited-under. Only the
// `framework` filter is forwarded; the page reads the whole report and
// filters it client-side, so it normally sends none.

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(req: Request) {
  const framework = new URL(req.url).searchParams.get("framework");
  const qs = framework ? `?framework=${encodeURIComponent(framework)}` : "";
  return noStore(await forwardJSON(`/v1/mapping-overlays/audited-under${qs}`));
}
