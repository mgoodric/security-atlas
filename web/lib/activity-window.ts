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
