// #2140 — pure helpers for the control-detail state strip.
//
// The strip shows the control's freshness and two markers: an exception
// covering it, and an open action plan. Each is derived here from the
// reads the page already makes, so vitest covers the rules without a DOM.

import type { ActionPlanRef } from "@/lib/api/action-plans";
import type { ControlStateEntry } from "@/lib/api/control-detail";
import type { Exception } from "@/lib/api/exceptions";

// Worst first: the strip reports the stalest cell, as the controls list
// does (#2131).
const FRESHNESS_RANK: Record<string, number> = {
  expired: 3,
  stale: 2,
  no_evidence: 1,
  fresh: 0,
};

/**
 * worstFreshness is the stalest freshness across the control's scope
 * cells, as a FreshnessState key, or null when there is no state. An
 * unknown value ranks below fresh, so it never hides a stale cell.
 */
export function worstFreshness(
  entries: readonly ControlStateEntry[] | undefined,
): string | null {
  if (!entries || entries.length === 0) return null;
  let worst: string | null = null;
  let rank = -1;
  for (const e of entries) {
    const r = FRESHNESS_RANK[e.freshness_status] ?? -1;
    if (r > rank) {
      rank = r;
      worst = e.freshness_status;
    }
  }
  return rank < 0 ? null : worst;
}

/** isExcepted reports whether any scope cell evaluated as excepted. */
export function isExcepted(
  entries: readonly ControlStateEntry[] | undefined,
): boolean {
  return (entries ?? []).some((e) => e.result === "excepted");
}

/**
 * exceptionsForControl keeps only the exceptions filed against this
 * control. The platform already filters `?control_id=`; this guard keeps
 * a page from listing another control's exception if it ever does not.
 */
export function exceptionsForControl(
  exceptions: readonly Exception[] | undefined,
  controlId: string,
): Exception[] {
  const want = controlId.toLowerCase();
  return (exceptions ?? []).filter((e) => e.control_id.toLowerCase() === want);
}

/** activeExceptions are the exceptions that cover the control now. */
export function activeExceptions(
  exceptions: readonly Exception[] | undefined,
): Exception[] {
  return (exceptions ?? []).filter((e) => e.status === "active");
}

// A plan still open is one not yet completed or verified.
const CLOSED_PLAN = new Set(["completed", "verified"]);

/** openActionPlans are the linked plans still in progress. */
export function openActionPlans(
  plans: readonly ActionPlanRef[] | undefined,
): ActionPlanRef[] {
  return (plans ?? []).filter((p) => !CLOSED_PLAN.has(p.status));
}
