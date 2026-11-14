// Slice 043 — posture stat tiles (per Plans/_archive/mockups/board-pack.html §01).
//
// Renders one tile per registered framework: coverage %, delta arrow, and
// a one-line state caption. Data is the slice-032 FrameworkPosture array
// inside the posture section. No fabricated numbers — if a framework has
// no posture row, the section degrades to a small "no posture data" note.
//
// Slice 222 — appends a methodology caption below the tiles documenting
// what the coverage number is (weighted SCF-anchored evidence pass rate
// intersected with each framework's scope predicate). The caption text
// lives in a sibling pure-TS module (posture-coverage-caption.ts) so a
// vitest node-env unit can pin the constitutional string without
// touching the JSX render path (web/vitest config disallows component
// rendering; see slice 069 / 130 / 183 precedent). AC-3: the caption
// renders only on the posture section because it lives inside this
// component, which is only mounted on key === "posture".

import Link from "next/link";

import { cn } from "@/lib/utils";

import { POSTURE_COVERAGE_CAPTION } from "./posture-coverage-caption";

// #2056: one row per framework version, carrying the dashboard's own
// per-framework posture (GET /v1/frameworks/posture). `delta` is the 90-day
// trend in percentage points. `scope_not_set` rows have no activated
// framework scope and carry no percentage.
export type FrameworkPostureRow = {
  framework_id?: string;
  framework_version_id?: string;
  slug: string;
  name: string;
  version?: string;
  scope_not_set?: boolean;
  coverage_pct: number;
  freshness_pct: number;
  baseline_pct?: number;
  trend_arrow: string;
  delta: number;
  state: string;
};

// One decimal, as the dashboard's framework posture tile shows it.
function pct(n: number): string {
  return Number(n.toFixed(1)).toString();
}

export function PostureTiles({
  frameworks,
}: {
  frameworks: FrameworkPostureRow[];
}) {
  if (!frameworks || frameworks.length === 0) {
    return (
      <>
        <p
          className="rounded-md border border-dashed border-slate-200 p-4 text-sm text-slate-500"
          data-testid="posture-empty"
        >
          No framework posture rows available for this pack.
        </p>
        <p
          className="mt-4 text-xs text-slate-500"
          data-testid="posture-coverage-caption"
        >
          {POSTURE_COVERAGE_CAPTION}
        </p>
      </>
    );
  }
  return (
    <>
      <div
        className="grid grid-cols-2 gap-3 md:grid-cols-4"
        data-testid="posture-tiles"
      >
        {frameworks.map((fw) => (
          <PostureTile
            key={fw.framework_version_id ?? fw.framework_id ?? fw.slug}
            fw={fw}
          />
        ))}
      </div>
      <p
        className="mt-4 text-xs text-slate-500"
        data-testid="posture-coverage-caption"
      >
        {POSTURE_COVERAGE_CAPTION}
      </p>
    </>
  );
}

function PostureTile({ fw }: { fw: FrameworkPostureRow }) {
  const label = fw.version ? `${fw.name} ${fw.version}` : fw.name;
  if (fw.scope_not_set) {
    return (
      <div
        className="rounded-lg border border-slate-200 p-4"
        data-testid="posture-tile"
      >
        <div className="text-[11px] uppercase tracking-wider text-slate-500">
          {label}
        </div>
        <div
          className="mt-1.5 text-lg font-semibold"
          data-testid="posture-scope-not-set"
        >
          Scope not set
        </div>
        {fw.framework_version_id ? (
          <Link
            href={`/framework-scopes/${encodeURIComponent(
              fw.framework_version_id,
            )}`}
            className="mt-2 inline-block text-xs underline"
          >
            Set the framework scope
          </Link>
        ) : null}
      </div>
    );
  }
  const delta = pct(fw.delta);
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
        {label}
      </div>
      <div className="mt-1.5 flex items-baseline gap-1.5">
        <span className="text-2xl font-semibold">{pct(fw.coverage_pct)}</span>
        <span className="text-lg font-normal text-slate-400">%</span>
        <span className={cn("text-xs font-medium", deltaTone)}>
          {fw.trend_arrow}{" "}
          {fw.delta > 0 ? `+${delta}` : fw.delta === 0 ? "flat" : delta}
        </span>
      </div>
      <div className={cn("mt-1 text-xs", stateTone)}>{fw.state}</div>
      <div className="mt-2 text-[11px] text-slate-400">
        evidence freshness {pct(fw.freshness_pct)}%
      </div>
    </div>
  );
}
