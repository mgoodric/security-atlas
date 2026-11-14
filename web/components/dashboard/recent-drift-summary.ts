// #2063 — the recent-drift panel's headline, derived from the same two
// lists the panel renders. The bare net delta used to sit beside the loss
// list ("delta +33" next to "no controls flipped out"), which read as a
// contradiction. Gains and losses are now counted from the lists
// themselves, so the headline and the lists cannot disagree.

import type { DriftReport } from "@/lib/api/dashboard";

export type DriftSummary = {
  gains: number;
  losses: number;
  // null when nothing changed in the window.
  label: string | null;
};

export function summarizeDrift(report: DriftReport): DriftSummary {
  const gains = report.started_passing?.length ?? 0;
  const losses = report.flipped_out.length;
  if (gains === 0 && losses === 0) {
    return { gains, losses, label: null };
  }
  return {
    gains,
    losses,
    label: `${gainsCount(gains)} started passing · ${losses} stopped passing`,
  };
}

// A gain count is signed only when there is a gain: "+3", but "0".
export function gainsCount(gains: number): string {
  return gains > 0 ? `+${gains}` : "0";
}
