// Slice 043 — pack cover header (per Plans/mockups/board-pack.html).
//
// The cover renders the report id, draft/published badge, title, period
// range subtitle, and a three-cell metadata strip (period, generated_at,
// approver). The mockup shows "DRAFT · 64% complete" — we render the
// approval-progress fraction honestly from the live pack (count of
// approved sections / total fixed sections).
//
// Slice 219 — the Author cell was dropped: the backend board-pack record
// has no author field, so the previous `value="—"` placeholder was a
// UI-honesty gap (em-dash reads as "missing data" when the field is
// not modeled). The mockup at Plans/mockups/board-pack.html line 69 still
// shows a 4-cell strip including Author; we intentionally diverge.
// Honesty > parity.

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { periodLabel } from "./pack-breadcrumb-segments";
import { PACK_HEADER_META_LABELS } from "./pack-header-meta";

type PackHeaderProps = {
  periodEnd: string;
  status: string;
  generatedAt: string;
  publishedBy?: string;
  approvedCount: number;
  totalSections: number;
};

export function PackHeader({
  periodEnd,
  status,
  generatedAt,
  publishedBy,
  approvedCount,
  totalSections,
}: PackHeaderProps) {
  const isPublished = status === "published";
  const completePct =
    totalSections === 0 ? 0 : Math.round((approvedCount * 100) / totalSections);

  return (
    <header className="mb-10" data-testid="pack-header">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <span className="font-mono text-xs uppercase tracking-wider text-slate-500">
          REPORT-{periodEnd}
        </span>
        <Badge
          className={cn(
            "rounded text-[10px] font-semibold uppercase tracking-wider",
            isPublished
              ? "bg-emerald-50 text-emerald-700 hover:bg-emerald-50"
              : "bg-amber-50 text-amber-700 hover:bg-amber-50",
          )}
        >
          {isPublished
            ? "published · frozen"
            : `draft · ${completePct}% complete`}
        </Badge>
      </div>
      <h1 className="mb-2 text-4xl font-semibold tracking-tight text-slate-900">
        {periodLabel(periodEnd)} Board Pack
      </h1>
      <p className="max-w-3xl text-slate-600">
        Period ending {periodEnd}. Posture, top risks, control coverage trend,
        and program asks for the upcoming board meeting.
      </p>
      <dl className="mt-6 grid grid-cols-2 gap-3 md:grid-cols-3">
        <MetaCell label={PACK_HEADER_META_LABELS[0]} value={periodEnd} mono />
        <MetaCell
          label={PACK_HEADER_META_LABELS[1]}
          value={formatTimestamp(generatedAt)}
          mono
        />
        <MetaCell
          label={PACK_HEADER_META_LABELS[2]}
          value={
            publishedBy && publishedBy.length > 0 ? publishedBy : "pending"
          }
          muted={!publishedBy}
        />
      </dl>
    </header>
  );
}

function MetaCell({
  label,
  value,
  mono,
  muted,
}: {
  label: string;
  value: string;
  mono?: boolean;
  muted?: boolean;
}) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-slate-500">
        {label}
      </dt>
      <dd
        className={cn(
          "mt-0.5 text-sm font-medium",
          mono && "font-mono",
          muted ? "text-slate-400" : "text-slate-900",
        )}
      >
        {value}
      </dd>
    </div>
  );
}

// periodLabel was moved to `./pack-breadcrumb-segments.ts` in slice 218
// so the breadcrumb (in the sticky export bar) and the cover header
// share one canonical implementation. Imported above.

function formatTimestamp(ts: string): string {
  if (!ts) return "—";
  // RFC3339 → "YYYY-MM-DD HH:MM UTC". Avoid Intl.DateTimeFormat to keep
  // server / client renders identical (no hydration mismatch).
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})/.exec(ts);
  return m ? `${m[1]} ${m[2]} UTC` : ts;
}
