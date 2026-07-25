// Slice 662 — §05 Vendor risk burndown panel.
//
// GENERATED section (slice 273): the board concern is overdue reviews on
// high-criticality vendors. The wire data carries three scalars plus a
// derived on-time percentage (see internal/board/pack.go SectionData
// `vendor_burndown_*`). This is a minimal structured visual styled to
// match the other simple panels (operational tiles / investment panel):
// total tracked, on-time/total, and past-due. An empty/zero-vendor tenant
// renders honest zeros / "—" — no fabrication (per the ATLAS-009 sibling).

import { cn } from "@/lib/utils";

type VendorBurndownPanelProps = {
  total?: number | null;
  onTime?: number | null;
  pastDue?: number | null;
  onTimePct?: number | null;
};

export function VendorBurndownPanel({
  total,
  onTime,
  pastDue,
  onTimePct,
}: VendorBurndownPanelProps) {
  const hasVendors = total != null && total > 0;
  return (
    <div
      className="grid grid-cols-2 gap-3 md:grid-cols-3"
      data-testid="vendor-burndown-panel"
    >
      <Tile
        label="High-criticality vendors"
        value={total != null ? `${total}` : "—"}
        hint=""
        tone="muted"
        testid="vendor-burndown-total"
      />
      <Tile
        label="Reviews on time"
        value={onTime != null && total != null ? `${onTime}/${total}` : "—"}
        hint={hasVendors && onTimePct != null ? `${onTimePct}% on time` : ""}
        tone={
          hasVendors && onTimePct != null && onTimePct >= 90 ? "good" : "muted"
        }
        testid="vendor-burndown-on-time"
      />
      <Tile
        label="Past due"
        value={pastDue != null ? `${pastDue}` : "—"}
        hint=""
        tone={pastDue != null && pastDue > 0 ? "warn" : "muted"}
        testid="vendor-burndown-past-due"
      />
    </div>
  );
}

function Tile({
  label,
  value,
  hint,
  tone,
  testid,
}: {
  label: string;
  value: string;
  hint: string;
  tone: "good" | "warn" | "muted";
  testid: string;
}) {
  return (
    <div
      className="rounded-lg border border-slate-200 p-4"
      data-testid={testid}
    >
      <div className="mb-1 text-xs text-slate-500">{label}</div>
      <div className="flex items-baseline gap-2">
        <span className="text-2xl font-semibold">{value}</span>
        {hint && (
          <span
            className={cn(
              "text-xs font-medium",
              tone === "good"
                ? "text-emerald-600"
                : tone === "warn"
                  ? "text-amber-600"
                  : "text-slate-500",
            )}
          >
            {hint}
          </span>
        )}
      </div>
    </div>
  );
}
