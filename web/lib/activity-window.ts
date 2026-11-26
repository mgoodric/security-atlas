// #2464 — the Activity page's default time window, in one place.
//
// The page, its export bar and anything else that reads the window take
// `from`/`to` from the same parsed filters, so the default is computed
// here once and every consumer sees the same pair.
//
// `to` is the exact instant the page opened, not a day boundary. The
// server reads `to` as exclusive, and the old default (today's 00:00 UTC)
// left every event from today out of the default view. An instant also
// sidesteps the viewer's timezone: "now" is the same moment everywhere.
// `from` stays at 00:00 UTC, DEFAULT_WINDOW_DAYS days back.

export const DEFAULT_WINDOW_DAYS = 7;

export type ActivityWindow = { from: string; to: string };

export function defaultActivityWindow(now: Date = new Date()): ActivityWindow {
  const from = new Date(now.getTime());
  from.setUTCDate(from.getUTCDate() - DEFAULT_WINDOW_DAYS);
  return { from: toRFC3339Day(from), to: now.toISOString() };
}

// toRFC3339Day is 00:00 UTC of the given instant's UTC day.
export function toRFC3339Day(d: Date): string {
  const yyyy = d.getUTCFullYear();
  const mm = String(d.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(d.getUTCDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}T00:00:00.000Z`;
}

// #2658 — the from/to date inputs speak the viewer's calendar, not UTC.
//
// A picked "From" day starts at the viewer's local midnight. A picked
// "To" day is inclusive: the server reads `to` as exclusive, so the
// bound sent is the start of the next local day. Reading a bound back
// into an input reverses that: `to` shows the local day of the instant
// just before it, so a picked day shows as itself and the default `to`
// (an exact instant) shows as the viewer's today.
//
// These read the viewer's timezone through Date's local accessors, so
// they belong in the browser; the page shows the UTC slice until it has
// hydrated.

function localMidnight(day: string, offsetDays: number): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) return null;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + offsetDays);
}

// fromBoundForDay is the start of the picked local day, or null for a
// cleared or malformed input.
export function fromBoundForDay(day: string): string | null {
  return localMidnight(day, 0)?.toISOString() ?? null;
}

// toBoundForDay is the start of the local day after the picked one, so
// the picked day is inside the window.
export function toBoundForDay(day: string): string | null {
  return localMidnight(day, 1)?.toISOString() ?? null;
}

function localDay(ms: number): string {
  const d = new Date(ms);
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

// fromInputValue is the local day `from` falls on.
export function fromInputValue(rfc3339: string): string {
  const t = Date.parse(rfc3339);
  return Number.isFinite(t) ? localDay(t) : "";
}

// toInputValue is the last local day inside a window ending at `to`.
export function toInputValue(rfc3339: string): string {
  const t = Date.parse(rfc3339);
  return Number.isFinite(t) ? localDay(t - 1) : "";
}
