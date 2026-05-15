// Slice 043 — operational metrics tiles (per board-pack.html §04).
//
// All four metrics are OPERATOR-ENTERED (slice 032 decision D3 — no v1
// data source). Each tile renders the value the operator typed or a
// "—" muted state when null. No fabrication.

import { cn } from "@/lib/utils";

type OperationalTilesProps = {
  phishingPassRatePct?: number | null;
  p1PatchMedianDays?: number | null;
  incidentCount?: number | null;
  vendorReviewsOnTime?: number | null;
  vendorReviewsTotal?: number | null;
};

export function OperationalTiles({
  phishingPassRatePct,
  p1PatchMedianDays,
  incidentCount,
  vendorReviewsOnTime,
  vendorReviewsTotal,
}: OperationalTilesProps) {
  return (
    <div
      className="grid grid-cols-2 gap-3 md:grid-cols-4"
      data-testid="operational-tiles"
    >
      <Tile
        label="Phishing pass rate"
        value={pct(phishingPassRatePct)}
        target="target ≥95%"
        tone={
          phishingPassRatePct != null && phishingPassRatePct >= 95
            ? "good"
            : "muted"
        }
      />
      <Tile
        label="P1 patch · median"
        value={p1PatchMedianDays != null ? `${p1PatchMedianDays} days` : "—"}
        target="target ≤7"
        tone={
          p1PatchMedianDays != null && p1PatchMedianDays <= 7 ? "good" : "muted"
        }
      />
      <Tile
        label="Incidents"
        value={incidentCount != null ? `${incidentCount}` : "—"}
        target=""
        tone="muted"
      />
      <Tile
        label="Vendor reviews on time"
        value={
          vendorReviewsOnTime != null && vendorReviewsTotal != null
            ? `${vendorReviewsOnTime}/${vendorReviewsTotal}`
            : "—"
        }
        target=""
        tone="muted"
      />
    </div>
  );
}

function pct(value?: number | null): string {
  return value == null ? "—" : `${value}%`;
}

function Tile({
  label,
  value,
  target,
  tone,
}: {
  label: string;
  value: string;
  target: string;
  tone: "good" | "muted";
}) {
  return (
    <div
      className="rounded-lg border border-slate-200 p-4"
      data-testid="operational-tile"
    >
      <div className="mb-1 text-xs text-slate-500">{label}</div>
      <div className="flex items-baseline gap-2">
        <span className="text-2xl font-semibold">{value}</span>
        {target && (
          <span
            className={cn(
              "text-xs font-medium",
              tone === "good" ? "text-emerald-600" : "text-slate-500",
            )}
          >
            {target}
          </span>
        )}
      </div>
    </div>
  );
}
