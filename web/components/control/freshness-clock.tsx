"use client";

// Slice 041 — freshness clock (AC-5).
//
// Binds to slice 012's GET /v1/controls/{id}/state. The issue's AC-5
// text references slice 016's `valid_until`, but slice 016 (evidence
// freshness + drift detection) is not on main. Slice 012's /state is the
// merged freshness surface and carries everything the clock needs:
// `freshness_status` (fresh|stale|...), `last_observed_at`,
// `freshness_class`, and `evidence_count_in_window` per scope cell.
// When slice 016 lands, the `valid_until` / drift overlay is additive.
//
// /state returns one entry per scope cell. The clock aggregates: it
// shows the MOST RECENT `last_observed_at` across cells (the freshest
// signal) and the WORST `freshness_status` (the weakest link) so a
// single stale cell is never hidden behind a fresh average.

import type {
  ControlStateResponse,
  ControlStateEntry,
} from "@/lib/api/control-detail";

// Severity ladder, most-degraded first. An unrecognized status sorts as
// most-degraded (rank -1) so a status the UI doesn't know about is never
// optimistically treated as "fresh".
const STATUS_RANK = ["expired", "stale", "aging", "fresh"];

function statusSeverity(status: string): number {
  return STATUS_RANK.indexOf(status);
}

// worstStatus returns the most-degraded freshness_status across all scope
// cells — the weakest link, so one stale cell is never hidden behind a
// fresh average.
function worstStatus(entries: ControlStateEntry[]): string | null {
  if (entries.length === 0) return null;
  return entries.reduce((worst, e) =>
    statusSeverity(e.freshness_status) < statusSeverity(worst.freshness_status)
      ? e
      : worst,
  ).freshness_status;
}

function mostRecentObserved(entries: ControlStateEntry[]): Date | null {
  let latest: Date | null = null;
  for (const e of entries) {
    if (!e.last_observed_at) continue;
    const d = new Date(e.last_observed_at);
    if (Number.isNaN(d.getTime())) continue;
    if (!latest || d > latest) latest = d;
  }
  return latest;
}

function humanizeSince(then: Date | null): string {
  if (!then) return "—";
  const ms = Date.now() - then.getTime();
  if (ms < 0) return "0m";
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

// statusRingColor maps the aggregate freshness status to the ring stroke.
function statusRingColor(status: string | null): string {
  switch (status) {
    case "fresh":
      return "rgb(16 185 129)";
    case "aging":
      return "rgb(245 158 11)";
    case "stale":
    case "expired":
      return "rgb(244 63 94)";
    default:
      return "rgb(148 163 184)";
  }
}

// The ring fill fraction: fresh = full, aging = two-thirds, stale/expired
// = one-third, unknown = empty. A coarse visual, not a precise gauge —
// the precise number is the "since latest evidence" readout beside it.
function statusFraction(status: string | null): number {
  switch (status) {
    case "fresh":
      return 1;
    case "aging":
      return 0.66;
    case "stale":
      return 0.33;
    case "expired":
      return 0.12;
    default:
      return 0;
  }
}

const RADIUS = 26;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

export function FreshnessClock({ state }: { state: ControlStateResponse }) {
  const entries = state.states;
  const status = worstStatus(entries);
  const latest = mostRecentObserved(entries);
  const ringColor = statusRingColor(status);
  const fraction = statusFraction(status);
  const dashOffset = CIRCUMFERENCE * (1 - fraction);

  const freshnessClass =
    entries.find((e) => e.freshness_class)?.freshness_class ?? "—";
  const totalRecords = entries.reduce(
    (sum, e) => sum + (e.evidence_count_in_window ?? 0),
    0,
  );
  const evaluatedCells = entries.length;

  return (
    <div data-testid="freshness-clock">
      <div className="flex items-center gap-4">
        <svg className="h-16 w-16" viewBox="0 0 64 64" aria-hidden="true">
          <circle
            cx={32}
            cy={32}
            r={RADIUS}
            fill="none"
            stroke="rgb(241 245 249)"
            strokeWidth={8}
          />
          <circle
            cx={32}
            cy={32}
            r={RADIUS}
            fill="none"
            stroke={ringColor}
            strokeWidth={8}
            strokeLinecap="round"
            strokeDasharray={CIRCUMFERENCE}
            strokeDashoffset={dashOffset}
            transform="rotate(-90 32 32)"
          />
        </svg>
        <div>
          <div
            className="text-2xl font-semibold leading-none"
            data-testid="freshness-since"
          >
            {humanizeSince(latest)}
          </div>
          <div className="mt-1 text-xs text-muted-foreground">
            since latest evidence
          </div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">
            status{" "}
            <span className="font-mono" data-testid="freshness-status">
              {status ?? "no evaluations"}
            </span>{" "}
            · class {freshnessClass}
          </div>
        </div>
      </div>
      <div className="mt-4 text-xs text-muted-foreground">
        <div className="flex justify-between border-t py-1">
          <span>Evaluated scope cells</span>
          <span className="font-mono text-foreground">{evaluatedCells}</span>
        </div>
        <div className="flex justify-between border-t py-1">
          <span>Evidence records in window</span>
          <span className="font-mono text-foreground">{totalRecords}</span>
        </div>
      </div>
    </div>
  );
}
