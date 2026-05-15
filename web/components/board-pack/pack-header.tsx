// Slice 043 — pack cover header (per Plans/mockups/board-pack.html).
//
// The cover renders the report id, draft/published badge, title, period
// range subtitle, and a four-cell metadata strip (period, generated_at,
// author, approver). The mockup shows "DRAFT · 64% complete" — we
// render the approval-progress fraction honestly from the live pack
// (count of approved sections / total fixed sections).

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

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
      <dl className="mt-6 grid grid-cols-2 gap-3 md:grid-cols-4">
        <MetaCell label="Period end" value={periodEnd} mono />
        <MetaCell label="Generated" value={formatTimestamp(generatedAt)} mono />
        <MetaCell label="Author" value="—" />
        <MetaCell
          label="Approver"
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

// periodLabel derives a "Q1 2026"-style label from YYYY-MM-DD when the
// date is a calendar-quarter end (Mar 31 / Jun 30 / Sep 30 / Dec 31).
// Otherwise it falls back to the raw date — no fabrication.
function periodLabel(periodEnd: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(periodEnd);
  if (!match) return periodEnd;
  const [, year, month, day] = match;
  const quarter = quarterFromMonthDay(month, day);
  return quarter ? `${quarter} ${year}` : periodEnd;
}

function quarterFromMonthDay(month: string, day: string): string | null {
  if (month === "03" && day === "31") return "Q1";
  if (month === "06" && day === "30") return "Q2";
  if (month === "09" && day === "30") return "Q3";
  if (month === "12" && day === "31") return "Q4";
  return null;
}

function formatTimestamp(ts: string): string {
  if (!ts) return "—";
  // RFC3339 → "YYYY-MM-DD HH:MM UTC". Avoid Intl.DateTimeFormat to keep
  // server / client renders identical (no hydration mismatch).
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})/.exec(ts);
  return m ? `${m[1]} ${m[2]} UTC` : ts;
}
