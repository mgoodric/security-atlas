// Slice 101 — pure formatter + color band for the acknowledgment-rate
// column on the /policies list view.
//
// The thresholds (>=95% green, 70-94% amber, <70% red) are derived
// from the SOC 2 CC1.4 norm where >90% acknowledgment is "compliant".
// Treat as a v1 default — captured in the slice 101 decisions log so
// the maintainer can tune post-deploy.
//
// Constitutional commitment: this module knows nothing about React.
// It is data-in, data-out so vitest can exercise it without React.
//
// Anti-criterion P0-A2 honored: this module formats an already-resolved
// PolicyAckRate. It does NOT fetch one. Per-row fan-out from the page
// to `/v1/policies/{id}/acknowledgment-rate` is forbidden — when the
// backend `?include=ack_rate` extension lands (spillover slice 107),
// the page passes the joined cell here.

import type { PolicyAckRate } from "@/lib/api";

export type AckRateBand = "green" | "amber" | "red" | "none";

/**
 * Band a percent in 0..100 into the three SOC 2 CC1.4 tiers. `null`
 * (no data) returns `none` so the page can render the em-dash placeholder
 * honestly (no fabricated band on a null value).
 */
export function ackRateBand(percent: number | null): AckRateBand {
  if (percent == null || !Number.isFinite(percent)) return "none";
  if (percent >= 95) return "green";
  if (percent >= 70) return "amber";
  return "red";
}

/**
 * Tailwind class for the inner indicator fill of the <Progress> bar.
 * Centralised so the emerald/amber/rose palette stays consistent with
 * the policies.html mockup (lines 191-271).
 */
export function ackRateColor(band: AckRateBand): string {
  switch (band) {
    case "green":
      return "bg-emerald-500";
    case "amber":
      return "bg-amber-500";
    case "red":
      return "bg-rose-500";
    case "none":
    default:
      return "bg-muted-foreground/30";
  }
}

/**
 * Tailwind class for the percent + numerator/denominator text caption
 * next to the bar. Mirrors the mockup (rose-700 for red band, slate-700
 * for green/amber, muted for the null placeholder).
 */
export function ackRateTextColor(band: AckRateBand): string {
  switch (band) {
    case "red":
      return "text-rose-700";
    case "green":
    case "amber":
      return "text-foreground";
    case "none":
    default:
      return "text-muted-foreground";
  }
}

/**
 * Format an ack-rate cell into the "98% · 142/145" caption string the
 * mockup pins. Returns "—" when the rate is null (no data, denominator
 * zero, or window unsettled) so the page renders honestly.
 *
 * The percent is rounded to the nearest integer (matches mockup).
 */
export function formatAckRate(rate: PolicyAckRate | null | undefined): string {
  if (rate == null) return "—";
  if (rate.percent == null || !Number.isFinite(rate.percent)) return "—";
  const pct = Math.round(rate.percent);
  return `${pct}% · ${rate.numerator}/${rate.denominator}`;
}

/**
 * Build the screen-reader label for the <Progress> bar. The mockup
 * caption is visual ("98% · 142/145"); the ARIA label is the long
 * form ("142 of 145 acknowledged · 98%") so assistive tech reads a
 * sentence the user can parse.
 */
export function ackRateAriaLabel(
  rate: PolicyAckRate | null | undefined,
): string {
  if (rate == null) return "Acknowledgment rate not available";
  if (rate.percent == null || !Number.isFinite(rate.percent)) {
    return "Acknowledgment rate not available";
  }
  const pct = Math.round(rate.percent);
  return `${rate.numerator} of ${rate.denominator} acknowledged · ${pct}%`;
}
