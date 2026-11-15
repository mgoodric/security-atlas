// Issue #2176 — the dashboard re-anchoring warning's copy, kept pure so
// vitest (node-only) can pin it.

import type { NeedsReanchoringResponse } from "@/lib/api/reanchoring";

export interface ReanchoringBannerText {
  count: number;
  title: string;
  description: string;
}

// reanchoringBannerText returns null when nothing needs re-anchoring, so
// the banner clears.
export function reanchoringBannerText(
  resp: NeedsReanchoringResponse,
): ReanchoringBannerText | null {
  const count = resp.controls.length;
  if (count === 0) return null;
  const releases = [...new Set(resp.controls.map((c) => c.current_release))];
  const target =
    releases.length === 1 ? `SCF ${releases[0]}` : "the current SCF release";
  const noun = count === 1 ? "control needs" : "controls need";
  return {
    count,
    title: `${count} ${noun} re-anchoring`,
    description: `${
      count === 1 ? "It is" : "They are"
    } still on an anchor from an earlier SCF release, so ${
      count === 1 ? "it earns" : "they earn"
    } no framework credit until re-anchored to ${target}.`,
  };
}
