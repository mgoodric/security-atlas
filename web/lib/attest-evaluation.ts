// #2111 — what the control page needs after an attestation: whether the
// evaluation it triggered has landed, and the control's rolled-up result.
//
// After an attestation the form returns to `/controls/<id>?attested=<the
// record's ingested_at>`. The control page shows "Evaluating…" until a
// state entry was evaluated at or after that instant, polling for at most
// ATTEST_EVALUATION_TIMEOUT_MS before it shows an error instead.

import type { ControlStateEntry } from "@/lib/api/control-detail";

/** How long the control page waits for the new state before giving up. */
export const ATTEST_EVALUATION_TIMEOUT_MS = 30_000;

/** How often the control page re-reads the state while it waits. */
export const ATTEST_EVALUATION_POLL_MS = 1_000;

/** The query parameter the attest form returns to the control with. */
export const ATTESTED_PARAM = "attested";

/**
 * evaluationLanded reports whether any state entry was evaluated at or
 * after `sinceISO`. An unparseable `sinceISO` counts as landed, so a
 * mangled URL never leaves the page waiting.
 */
export function evaluationLanded(
  entries: ControlStateEntry[] | undefined,
  sinceISO: string,
): boolean {
  const since = Date.parse(sinceISO);
  if (Number.isNaN(since)) return true;
  if (!entries) return false;
  return entries.some((e) => {
    const at = Date.parse(e.evaluated_at);
    return !Number.isNaN(at) && at >= since;
  });
}

/**
 * rollupResult folds the per-scope-cell results into one, with the
 * engine's precedence: any fail, then any pass, then excepted, then
 * inconclusive, else na. Null when there is no state at all.
 */
export function rollupResult(
  entries: ControlStateEntry[] | undefined,
): string | null {
  if (!entries || entries.length === 0) return null;
  const seen = new Set(entries.map((e) => e.result));
  for (const r of ["fail", "pass", "excepted", "inconclusive"]) {
    if (seen.has(r)) return r;
  }
  return "na";
}

/** isManualImplementation is true for the controls a person attests. */
export function isManualImplementation(
  implementationType: string | undefined,
): boolean {
  return (
    implementationType === "manual_attested" ||
    implementationType === "manual_periodic"
  );
}
