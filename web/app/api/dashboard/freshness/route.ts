import { getEvidenceFreshness } from "@/lib/api";
import { dashboardProxy } from "../proxy";

// Slice 040 — server-side proxy for GET /v1/evidence/freshness
// (slice 016 freshness read model, by-class distribution).

export function GET() {
  return dashboardProxy(getEvidenceFreshness);
}
