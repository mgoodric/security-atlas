import { forwardJSON } from "@/lib/api/bff";

// #1794 — GET /v1/frameworks (id, name, slug, latest_version_id). The SoA
// tab reads it to title an ISO/IEC 27001 version "Statement of
// Applicability" and every other framework "Applicability".

export async function GET() {
  return forwardJSON("/v1/frameworks");
}
