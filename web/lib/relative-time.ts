// Slice 255 — relative-time formatter for header "last evaluated" timestamps.
//
// Mirrors the freshness-clock's `humanizeSince` helper
// (`web/components/control/freshness-clock.tsx`) but expresses the result
// in operator-facing copy ("8 minutes ago", "3 days ago", "just now")
// rather than the compact ring-readout form ("8m", "3d"). The two are
// deliberately separate: the freshness-clock's compact form fits inside
// the SVG ring readout, while the header sub-line is full sentence-form
// per the mockup (`Plans/_archive/mockups/control.html` line 100 — "last evaluated
// 8 minutes ago").
//
// Inputs:
//   - ISO 8601 timestamp string, or `null` / `undefined`.
//   - optional `now` override (test seam — defaults to `Date.now()`).
//
// Returns:
//   - "—" for null / undefined / unparsable input.
//   - "never" for explicit-null intent — caller decides between this and
//     "—" at the call site (see `relativeTimeOrNever`).
//   - "just now" for any timestamp within the last minute.
//   - "N minute(s) ago" / "N hour(s) ago" / "N day(s) ago" otherwise.
//   - timestamps in the future (clock skew / pre-seed data) clamp to
//     "just now" rather than rendering a negative duration. The slice
//     012 evaluator is deterministic about not back-dating, but
//     defense-in-depth is cheap here.
//
// This is a pure helper (no React, no hooks) — vitest-coverable per AC-5
// without spinning up jsdom.

export function relativeTime(
  iso: string | null | undefined,
  now: number = Date.now(),
): string {
  if (!iso) return "—";
  const then = new Date(iso);
  const t = then.getTime();
  if (Number.isNaN(t)) return "—";

  const deltaMs = now - t;
  // Clamp future timestamps (clock skew tolerance) to "just now" rather
  // than rendering a negative count.
  if (deltaMs < 60_000) return "just now";

  const minutes = Math.floor(deltaMs / 60_000);
  if (minutes < 60) {
    return `${minutes} ${minutes === 1 ? "minute" : "minutes"} ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} ${hours === 1 ? "hour" : "hours"} ago`;
  }
  const days = Math.floor(hours / 24);
  return `${days} ${days === 1 ? "day" : "days"} ago`;
}

// relativeTimeOrNever distinguishes "we have no state record at all"
// (caller passes `undefined` → "—") from "state record exists but the
// underlying field is null" (caller passes `null` → "never"). The
// slice 255 control-header sub-line uses the latter for the
// state-exists-but-no-evidence case.
export function relativeTimeOrNever(
  iso: string | null | undefined,
  now: number = Date.now(),
): string {
  if (iso === null) return "never";
  return relativeTime(iso, now);
}

// #2084 — the one date formatter every page renders timestamps through.
//
// `formatTimestamp` picks the visible text; `<Timestamp>`
// (`components/ui/timestamp.tsx`) renders it inside a `<time>` whose
// tooltip is the exact instant in UTC. Three styles:
//
//   - "recency" — activity- and recency-style stamps (last observed,
//     activity rows). Within RECENT_WINDOW_MS of `now` it reads as
//     `relativeTime` ("2 days ago"); otherwise the absolute date and time.
//     A stamp more than a minute in the future is never "recent": it
//     renders absolute, so clock skew can't hide as "just now".
//   - "date" — the calendar day ("14 Nov 2026"). Due dates, review
//     dates and period bounds use this: they never go relative.
//   - "datetime" — the absolute day plus the time of day.
//
// Instants ("recency", "datetime") render in the viewer's locale and
// timezone (the Intl defaults) unless `locale` / `timeZone` override
// them. A calendar day — a date-only value ("2026-11-14"), or any value
// in "date" style — is not an instant: the backend carries it as
// midnight UTC, so it formats in UTC and no viewer timezone can move it
// to the neighbouring day. A date-only value is always a calendar day,
// whatever the style.

export type TimestampStyle = "recency" | "date" | "datetime";

export const RECENT_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

export type TimestampFormatOptions = {
  style?: TimestampStyle;
  now?: number;
  locale?: string;
  timeZone?: string;
};

export type FormattedTimestamp = {
  // Visible text; "—" for null, empty or unparsable input.
  text: string;
  // Tooltip: the exact instant in UTC, labelled ("2026-11-14 09:30:05
  // UTC"), or the bare day for a date-only value. Empty when invalid.
  title: string;
  // The machine-readable value for `<time dateTime>`. Empty when invalid.
  dateTime: string;
};

const INVALID: FormattedTimestamp = { text: "—", title: "", dateTime: "" };

function parse(value: string): { t: number; dateOnly: boolean } | null {
  const dateOnly = DATE_ONLY.test(value);
  const t = new Date(dateOnly ? `${value}T00:00:00Z` : value).getTime();
  return Number.isNaN(t) ? null : { t, dateOnly };
}

// exactUTC renders the tooltip text: the instant to the second, in UTC,
// labelled so an auditor never reads it as local time.
export function exactUTC(value: string | null | undefined): string {
  if (!value) return "";
  const p = parse(value);
  if (!p) return "";
  const iso = new Date(p.t).toISOString();
  if (p.dateOnly) return iso.slice(0, 10);
  return `${iso.slice(0, 10)} ${iso.slice(11, 19)} UTC`;
}

function absolute(
  t: number,
  withTime: boolean,
  locale: string | undefined,
  timeZone: string | undefined,
): string {
  return new Intl.DateTimeFormat(locale, {
    day: "numeric",
    month: "short",
    year: "numeric",
    ...(withTime ? { hour: "numeric", minute: "2-digit" } : {}),
    timeZone,
  }).format(t);
}

export function formatTimestamp(
  value: string | null | undefined,
  opts: TimestampFormatOptions = {},
): FormattedTimestamp {
  if (!value) return INVALID;
  const p = parse(value);
  if (!p) return INVALID;
  const { style = "recency", now = Date.now(), locale } = opts;
  const title = exactUTC(value);

  if (p.dateOnly || style === "date") {
    return {
      text: absolute(p.t, false, locale, "UTC"),
      title,
      dateTime: value,
    };
  }
  if (style === "recency") {
    const delta = now - p.t;
    if (delta > -60_000 && delta < RECENT_WINDOW_MS) {
      return { text: relativeTime(value, now), title, dateTime: value };
    }
  }
  return {
    text: absolute(p.t, true, locale, opts.timeZone),
    title,
    dateTime: value,
  };
}
