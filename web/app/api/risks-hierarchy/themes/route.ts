import { getRiskThemes } from "@/lib/api/risk-hierarchy";
import { hierarchyProxy } from "../proxy";

// Slice 056 — server-side proxy for GET /v1/themes (slice 053 theme
// vocabulary). Supplies the heatmap's x-axis columns: 10 default themes
// plus any tenant-private ones. Pure read-only.

export function GET() {
  return hierarchyProxy((bearer) =>
    getRiskThemes(bearer).then((themes) => ({ themes, count: themes.length })),
  );
}
