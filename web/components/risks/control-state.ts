// #2138 — a linked control's live state on the risk page's Mitigation
// section, from GET /v1/controls/{id}/state (one entry per scope cell).
//
// Pure, so vitest pins it. The rollup is worst-cell-wins, the rule the
// /controls list applies in SQL (`worst_per_control`,
// internal/db/queries/controls.sql): fail > inconclusive > excepted >
// pass > na, and expired > stale > no_evidence > fresh. A control with no
// evaluation yet has no state.

import type { ControlStateEntry } from "@/lib/api/control-detail";

const RESULT_RANK: Record<string, number> = {
  fail: 5,
  inconclusive: 4,
  excepted: 3,
  pass: 2,
  na: 1,
};

const FRESHNESS_RANK: Record<string, number> = {
  expired: 4,
  stale: 3,
  no_evidence: 2,
  fresh: 1,
};

export type ControlLiveState = { result: string; freshness: string | null };

// The Badge variant for a result, the /controls list's mapping (#2112:
// excepted is never the pass hue). Unknown values stay neutral.
const RESULT_VARIANT = {
  pass: "pass",
  fail: "critical",
  inconclusive: "warning",
  na: "secondary",
  excepted: "info",
} as const;

export function resultBadgeVariant(
  result: string,
): (typeof RESULT_VARIANT)[keyof typeof RESULT_VARIANT] | "outline" {
  return RESULT_VARIANT[result as keyof typeof RESULT_VARIANT] ?? "outline";
}

function worst(
  values: readonly string[],
  rank: Record<string, number>,
): string | null {
  let best: string | null = null;
  for (const v of values) {
    const r = rank[v];
    if (r === undefined) continue;
    if (best === null || r > rank[best]) best = v;
  }
  return best;
}

/** The control's worst result and freshness, or null when it has none. */
export function controlLiveState(
  states: readonly Pick<ControlStateEntry, "result" | "freshness_status">[],
): ControlLiveState | null {
  const result = worst(
    states.map((s) => s.result),
    RESULT_RANK,
  );
  if (result === null) return null;
  return {
    result,
    freshness: worst(
      states.map((s) => s.freshness_status),
      FRESHNESS_RANK,
    ),
  };
}
