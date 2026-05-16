// Slice 097 — value-formatting helpers for the metrics dashboard.
//
// Numeric values arrive from the wire as strings (Postgres NUMERIC ->
// JSON string). Each metric carries a `unit` string like "percent",
// "days", "count", "dollars". Format per the metric's unit so the
// dashboard renders "94%" rather than the raw "0.94".

export function parseValue(s?: string): number | undefined {
  if (s === undefined || s === null || s === "") return undefined;
  const n = Number(s);
  return Number.isFinite(n) ? n : undefined;
}

export function formatValue(value: number | undefined, unit: string): string {
  if (value === undefined) return "—";
  switch (unit) {
    case "percent":
    case "ratio":
      // Treat values <= 1 as a fraction, values > 1 as already-scaled
      // percentage points. The slice-076 evaluators emit fractions
      // (0..1); the manual_input metrics may emit either.
      return value <= 1
        ? `${(value * 100).toFixed(1)}%`
        : `${value.toFixed(1)}%`;
    case "days":
      return `${value.toFixed(1)} d`;
    case "hours":
      return `${value.toFixed(1)} h`;
    case "count":
      return Number.isInteger(value) ? String(value) : value.toFixed(1);
    case "dollars":
    case "usd":
      return `$${value.toLocaleString(undefined, {
        maximumFractionDigits: 0,
      })}`;
    default:
      return value.toLocaleString(undefined, { maximumFractionDigits: 2 });
  }
}

export function formatRelative(iso: string): string {
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return iso;
  const ageMs = Date.now() - t;
  const days = Math.floor(ageMs / (24 * 60 * 60 * 1000));
  if (days <= 0) {
    const hours = Math.max(0, Math.floor(ageMs / (60 * 60 * 1000)));
    if (hours === 0) return "just now";
    return `${hours}h ago`;
  }
  if (days === 1) return "1 day ago";
  if (days < 30) return `${days} days ago`;
  if (days < 365) return `${Math.floor(days / 30)} mo ago`;
  return `${Math.floor(days / 365)} yr ago`;
}
