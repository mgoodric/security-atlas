// #1778 — GET /v1/admin/crosswalk-edge-remap-proposals proxy, read by the
// remap review page. Crosswalk edges are global catalog data, so the
// platform admits super-admins only (handler and OPA); a tenant admin gets
// 403. The BFF forwards the caller's own bearer, never a service
// credential, and passes the 403 through: the page hides the edge section
// on it, but the platform is the gate.

import { forwardJSON } from "@/lib/api/bff";

export function GET() {
  return forwardJSON(`/v1/admin/crosswalk-edge-remap-proposals?status=open`);
}
