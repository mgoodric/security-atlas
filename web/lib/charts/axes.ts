// #2095 — pure axis math for the shared chart wrapper
// (`components/charts/axis-chart.tsx`). No JSX, so vitest (node env)
// covers it directly.
//
// Two jobs: fit a y-axis to the data with round tick values, and pick
// dated x-axis ticks across a time span. Tick labels are drawn in the
// wrapper's margins, outside the plot area, so they never sit on top of
// a mark.

export type LinearAxis = {
  min: number;
  max: number;
  ticks: number[];
};

// niceStep rounds a raw step up to 1, 2, 2.5 or 5 times a power of ten.
function niceStep(raw: number): number {
  const exp = Math.floor(Math.log10(raw));
  const base = 10 ** exp;
  const f = raw / base;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10;
  return nice * base;
}

// linearAxis fits a y-axis to [lo, hi]: the domain is widened to the
// nearest round step either side, and the ticks are every step in it.
// A flat series (lo === hi) gets a band around its value so the line
// sits mid-plot instead of on an edge.
export function linearAxis(lo: number, hi: number, count = 5): LinearAxis {
  if (!Number.isFinite(lo) || !Number.isFinite(hi)) {
    return { min: 0, max: 1, ticks: [0, 0.5, 1] };
  }
  if (lo > hi) [lo, hi] = [hi, lo];
  if (lo === hi) {
    const pad = lo === 0 ? 1 : Math.abs(lo) * 0.1;
    lo -= pad;
    hi += pad;
  }
  const step = niceStep((hi - lo) / Math.max(1, count - 1));
  // Round to the step's precision so 0.1 + 0.2 reads 0.3.
  const decimals = Math.max(0, -Math.floor(Math.log10(step)) + 1);
  const round = (v: number) => Number(v.toFixed(decimals));
  const first = Math.floor(lo / step);
  const last = Math.ceil(hi / step);
  const ticks: number[] = [];
  for (let i = first; i <= last; i++) ticks.push(round(i * step));
  return { min: ticks[0], max: ticks[ticks.length - 1], ticks };
}

export type TimeAxis = {
  min: number;
  max: number;
  ticks: number[];
};

const DAY_MS = 24 * 60 * 60 * 1000;

// timeAxis spreads `count` evenly spaced ticks over [t0, t1]
// (epoch ms), endpoints included. A single observation gets a one-day
// window centred on it so there is still a dated axis to read.
export function timeAxis(t0: number, t1: number, count = 4): TimeAxis {
  if (t0 > t1) [t0, t1] = [t1, t0];
  if (t0 === t1) {
    t0 -= DAY_MS / 2;
    t1 += DAY_MS / 2;
  }
  const n = Math.max(2, count);
  const ticks = Array.from(
    { length: n },
    (_, i) => t0 + ((t1 - t0) * i) / (n - 1),
  );
  return { min: t0, max: t1, ticks };
}

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

// formatDateTick labels an x tick. Spans of two days or more show the
// date ("Nov 3"); shorter spans add the UTC hour ("Nov 3 14:00") so
// ticks inside one day are distinguishable. UTC keeps the label the
// same for every viewer and in tests.
export function formatDateTick(t: number, spanMs: number): string {
  const d = new Date(t);
  const date = `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}`;
  if (spanMs >= 2 * DAY_MS) return date;
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  return `${date} ${hh}:${mm}`;
}
