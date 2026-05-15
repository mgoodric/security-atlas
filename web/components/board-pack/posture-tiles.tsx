// Slice 043 — posture stat tiles (per Plans/mockups/board-pack.html §01).
//
// Renders one tile per registered framework: coverage %, delta arrow, and
// a one-line state caption. Data is the slice-032 FrameworkPosture array
// inside the posture section. No fabricated numbers — if a framework has
// no posture row, the section degrades to a small "no posture data" note.

import { cn } from "@/lib/utils";

export type FrameworkPostureRow = {
  slug: string;
  name: string;
  coverage_pct: number;
  freshness_pct: number;
  trend_arrow: string;
  delta: number;
  state: string;
};

export function PostureTiles({
  frameworks,
}: {
  frameworks: FrameworkPostureRow[];
}) {
  if (!frameworks || frameworks.length === 0) {
    return (
      <p
        className="rounded-md border border-dashed border-slate-200 p-4 text-sm text-slate-500"
        data-testid="posture-empty"
      >
        No framework posture rows available for this pack.
      </p>
    );
  }
  return (
    <div
      className="grid grid-cols-2 gap-3 md:grid-cols-4"
      data-testid="posture-tiles"
    >
      {frameworks.map((fw) => (
        <PostureTile key={fw.slug} fw={fw} />
      ))}
    </div>
  );
}

function PostureTile({ fw }: { fw: FrameworkPostureRow }) {
  const deltaTone =
    fw.delta > 0
      ? "text-emerald-600"
      : fw.delta < 0
        ? "text-rose-600"
        : "text-slate-500";
  const stateTone =
    fw.state === "audit-ready"
      ? "text-emerald-700"
      : fw.state === "regressed"
        ? "text-rose-700"
        : "text-amber-700";
  return (
    <div
      className="rounded-lg border border-slate-200 p-4"
      data-testid="posture-tile"
    >
      <div className="text-[11px] uppercase tracking-wider text-slate-500">
        {fw.name}
      </div>
      <div className="mt-1.5 flex items-baseline gap-1.5">
        <span className="text-2xl font-semibold">{fw.coverage_pct}</span>
        <span className="text-lg font-normal text-slate-400">%</span>
        <span className={cn("text-xs font-medium", deltaTone)}>
          {fw.trend_arrow}{" "}
          {fw.delta > 0 ? `+${fw.delta}` : fw.delta === 0 ? "flat" : fw.delta}
        </span>
      </div>
      <div className={cn("mt-1 text-xs", stateTone)}>{fw.state}</div>
      <div className="mt-2 text-[11px] text-slate-400">
        evidence freshness {fw.freshness_pct}%
      </div>
    </div>
  );
}
