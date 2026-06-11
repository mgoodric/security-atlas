"use client";

// Slice 094 — month-grid view (toggle).
// Slice 183 — `linkFor` extracted to `./link-for`; exception / policy
// rows in the day popover now render as a non-linked span with a
// tooltip instead of an `<a>` to a route that 404s.
//
// Standard 7-column × N-row month calendar. The grid is hand-rolled
// per anti-criterion P0-A6 (no FullCalendar / react-big-calendar). Day
// cells are computed by walking from the Sunday before the 1st through
// the Saturday after the last day of the month.
//
// Click on a day opens an inline popover listing that day's events.
// Each event row's link disposition is the shared `linkFor` helper —
// see `./link-for.ts` for the routing map.

import Link from "next/link";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import type { CalendarEvent } from "@/lib/api/calendar";

import { linkFor } from "./link-for";

type Props = {
  events: CalendarEvent[];
  anchor: Date; // first-of-month
  onPrev: () => void;
  onNext: () => void;
};

const TYPE_COLOR: Record<string, string> = {
  audit: "bg-blue-500",
  exception: "bg-amber-500",
  policy: "bg-purple-500",
  vendor: "bg-rose-500",
  control: "bg-emerald-500",
};

const WEEKDAY_HEADERS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function isoDay(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(
    2,
    "0",
  )}-${String(d.getDate()).padStart(2, "0")}`;
}

function startOfWeek(d: Date): Date {
  const r = new Date(d);
  r.setDate(d.getDate() - d.getDay());
  return r;
}

function endOfWeek(d: Date): Date {
  const r = new Date(d);
  r.setDate(d.getDate() + (6 - d.getDay()));
  return r;
}

function lastOfMonth(anchor: Date): Date {
  return new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
}

function eventsByDay(events: CalendarEvent[]): Map<string, CalendarEvent[]> {
  const m = new Map<string, CalendarEvent[]>();
  for (const ev of events) {
    const d = new Date(ev.starts_at);
    const key = isoDay(d);
    const arr = m.get(key) ?? [];
    arr.push(ev);
    m.set(key, arr);
  }
  return m;
}

export function MonthGridView({ events, anchor, onPrev, onNext }: Props) {
  const [openDay, setOpenDay] = useState<string | null>(null);
  const byDay = eventsByDay(events);

  const monthLabel = anchor.toLocaleDateString(undefined, {
    year: "numeric",
    month: "long",
  });

  const gridStart = startOfWeek(anchor);
  const gridEnd = endOfWeek(lastOfMonth(anchor));
  const days: Date[] = [];
  const cur = new Date(gridStart);
  while (cur <= gridEnd) {
    days.push(new Date(cur));
    cur.setDate(cur.getDate() + 1);
  }

  return (
    <div className="rounded-md border bg-card">
      <div className="flex items-center justify-between border-b p-3">
        <Button variant="outline" size="sm" onClick={onPrev}>
          ‹
        </Button>
        <h2 className="text-sm font-semibold">{monthLabel}</h2>
        <Button variant="outline" size="sm" onClick={onNext}>
          ›
        </Button>
      </div>
      <div className="grid grid-cols-7 border-b text-xs font-medium text-muted-foreground">
        {WEEKDAY_HEADERS.map((d) => (
          <div key={d} className="p-2 text-center">
            {d}
          </div>
        ))}
      </div>
      <div className="grid grid-cols-7">
        {days.map((d) => {
          const key = isoDay(d);
          const isCurMonth = d.getMonth() === anchor.getMonth();
          const dayEvents = byDay.get(key) ?? [];
          const overdueDot = dayEvents.some(
            (e) => e.type === "control" && e.status === "overdue",
          );
          return (
            <button
              key={key}
              type="button"
              onClick={() => setOpenDay(openDay === key ? null : key)}
              className={`relative min-h-[5rem] border-b border-r p-2 text-left text-sm transition-colors hover:bg-muted/40 ${
                isCurMonth ? "" : "text-muted-foreground/60 bg-muted/20"
              } ${openDay === key ? "ring-2 ring-foreground/20" : ""}`}
            >
              <div className="flex items-center justify-between">
                <span className={isCurMonth ? "font-medium" : ""}>
                  {d.getDate()}
                </span>
                {overdueDot && (
                  <span
                    aria-label="overdue"
                    className="inline-block h-2 w-2 rounded-full bg-red-500"
                  />
                )}
              </div>
              {dayEvents.length > 0 && (
                <ul className="mt-1 space-y-0.5">
                  {dayEvents.slice(0, 3).map((ev) => (
                    <li
                      key={`${ev.type}-${ev.id}`}
                      className="flex items-center gap-1 truncate text-xs"
                    >
                      <span
                        aria-hidden
                        className={`inline-block h-1.5 w-1.5 rounded-full ${
                          TYPE_COLOR[ev.type] ?? "bg-muted"
                        }`}
                      />
                      <span className="truncate">{ev.title}</span>
                    </li>
                  ))}
                  {dayEvents.length > 3 && (
                    <li className="text-xs text-muted-foreground">
                      +{dayEvents.length - 3} more
                    </li>
                  )}
                </ul>
              )}
            </button>
          );
        })}
      </div>

      {openDay && (
        <div
          role="dialog"
          aria-label={`Events on ${openDay}`}
          className="border-t bg-muted/30 p-4"
        >
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">
              {new Date(openDay).toLocaleDateString(undefined, {
                weekday: "long",
                year: "numeric",
                month: "long",
                day: "numeric",
              })}
            </h3>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setOpenDay(null)}
              aria-label="close"
            >
              Close
            </Button>
          </div>
          <ul className="mt-2 divide-y rounded-md border bg-card">
            {(byDay.get(openDay) ?? []).map((ev) => {
              const target = linkFor(ev);
              const rowBody = (
                <>
                  <span
                    aria-hidden
                    className={`mt-1.5 inline-block h-2.5 w-2.5 rounded-full ${
                      TYPE_COLOR[ev.type] ?? "bg-muted"
                    }`}
                  />
                  <div className="flex-1">
                    <p className="text-sm font-medium">{ev.title}</p>
                    <p className="text-xs text-muted-foreground">
                      {ev.type} · {ev.status}
                      {ev.cadence ? ` · ${ev.cadence}` : ""}
                    </p>
                  </div>
                </>
              );
              return (
                <li key={`${ev.type}-${ev.id}`} className="p-3">
                  {target.kind === "link" ? (
                    <Link
                      href={target.href}
                      className="flex items-start gap-3 -m-3 p-3 hover:bg-muted/50 rounded-md transition-colors"
                    >
                      {rowBody}
                    </Link>
                  ) : (
                    // Slice 183 — no `data-testid` here on purpose; see
                    // `agenda-view.tsx` for the rationale (would trip
                    // the slice 178 HONESTY-GAP heuristic, manifest is
                    // frozen per P0-183-3).
                    <span
                      title={target.reason}
                      aria-label={target.reason}
                      className="flex items-start gap-3 -m-3 p-3 rounded-md cursor-help"
                    >
                      {rowBody}
                    </span>
                  )}
                </li>
              );
            })}
            {(byDay.get(openDay) ?? []).length === 0 && (
              <li className="p-3 text-sm text-muted-foreground">
                No events on this day.
              </li>
            )}
          </ul>
        </div>
      )}
    </div>
  );
}
