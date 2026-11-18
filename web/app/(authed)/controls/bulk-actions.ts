// #2141 — the /controls bulk actions, as pure logic the node-env vitest
// runner covers (slice 069 P0-A3: no JSX, no DOM here).
//
// Each bulk action goes through a write the platform already gates; none
// adds a route:
//   - assign owner: POST /v1/controls:bulk-assign-owner (slice 468). The
//     server re-checks role + tenant for every id and applies all or none.
//   - evaluate now: POST /v1/controls/{id}/evaluate, once per control.
//   - link to an action plan: POST /v1/action-plans/{id}/controls/{cid},
//     once per control.
//   - request exception: the request form, opened for the selection; it
//     files one exception per control through POST /v1/exceptions.
// The per-control writes report per item: n succeeded, m refused with the
// platform's reason. See docs/audit-log/2141-controls-bulk-actions-decisions.md.

import type { MeProfile } from "@/lib/api/me";

/** Which bulk actions the caller is offered. */
export type BulkActionGates = {
  assignOwner: boolean;
  evaluate: boolean;
  linkPlan: boolean;
  requestException: boolean;
};

export const NO_BULK_ACTIONS: BulkActionGates = {
  assignOwner: false,
  evaluate: false,
  linkPlan: false,
  requestException: false,
};

/**
 * The bulk actions the caller may take, from the live /v1/me and the
 * caller's modules. Fails closed: no profile (still loading, or the read
 * failed) offers nothing. The rules match the control page (#2140):
 * assign owner, link to a plan and request exception need an approver;
 * evaluate also admits a control owner, whom the handler then checks
 * against each control's assignment. Link to a plan needs the module
 * that owns action plans, request exception the Exceptions module.
 */
export function bulkActionGates(
  me: Pick<MeProfile, "is_approver" | "roles"> | undefined,
  modules: { actionPlans: boolean; exceptions: boolean },
): BulkActionGates {
  if (!me) return NO_BULK_ACTIONS;
  const approver = me.is_approver === true;
  const controlOwner = (me.roles ?? []).includes("control_owner");
  return {
    assignOwner: approver,
    evaluate: approver || controlOwner,
    linkPlan: approver && modules.actionPlans,
    requestException: approver && modules.exceptions,
  };
}

/** True when the caller is offered at least one bulk action. */
export function anyBulkAction(gates: BulkActionGates): boolean {
  return (
    gates.assignOwner ||
    gates.evaluate ||
    gates.linkPlan ||
    gates.requestException
  );
}

/** One refused item: the control and the platform's reason. */
export type Refusal = { id: string; reason: string };

/** The outcome of a per-control bulk write. */
export type PerItemResult = {
  succeeded: string[];
  refused: Refusal[];
};

// How many per-control writes run at once. Small: the platform rate-limits
// evaluation per principal, and a bounded fan-out keeps a 200-control
// batch from opening 200 sockets.
export const BULK_CONCURRENCY = 4;

/**
 * Runs `write` once per id, at most `concurrency` at a time, and sorts
 * each id into succeeded or refused. A thrown error is a refusal carrying
 * its message; nothing short-circuits the rest of the batch. The result
 * keeps the input order.
 */
export async function runPerItem(
  ids: readonly string[],
  write: (id: string) => Promise<unknown>,
  concurrency = BULK_CONCURRENCY,
): Promise<PerItemResult> {
  const outcome: (Refusal | null)[] = new Array(ids.length);
  let next = 0;
  async function worker() {
    while (next < ids.length) {
      const i = next++;
      try {
        await write(ids[i]);
        outcome[i] = null;
      } catch (err) {
        outcome[i] = {
          id: ids[i],
          reason: err instanceof Error && err.message ? err.message : "Refused",
        };
      }
    }
  }
  const n = Math.max(1, Math.min(concurrency, ids.length));
  await Promise.all(Array.from({ length: n }, worker));
  const succeeded: string[] = [];
  const refused: Refusal[] = [];
  outcome.forEach((o, i) => {
    if (o) refused.push(o);
    else succeeded.push(ids[i]);
  });
  return { succeeded, refused };
}

/**
 * The one-line summary of a per-item result: "3 controls evaluated." or
 * "2 controls linked to the plan, 1 refused." `done` is the past-tense
 * phrase for a success.
 */
export function summarize(result: PerItemResult, done: string): string {
  const n = result.succeeded.length;
  const m = result.refused.length;
  const head = `${n} control${n === 1 ? "" : "s"} ${done}`;
  return m === 0 ? `${head}.` : `${head}, ${m} refused.`;
}

/** The bulk result region's message. */
export type BulkMessage = {
  kind: "ok" | "error";
  text: string;
  /** Per-item refusals, labelled for display. */
  refusals?: { label: string; reason: string }[];
};

/** Builds the region's message from a per-item result. */
export function perItemMessage(
  result: PerItemResult,
  done: string,
  labelOf: (id: string) => string,
): BulkMessage {
  return {
    kind: result.refused.length === 0 ? "ok" : "error",
    text: summarize(result, done),
    refusals: result.refused.map((r) => ({
      label: labelOf(r.id),
      reason: r.reason,
    })),
  };
}

/** The request-exception form, opened for the selected controls. */
export function exceptionRequestHref(ids: readonly string[]): string {
  const qs = new URLSearchParams();
  for (const id of ids) qs.append("control_id", id);
  return `/exceptions/new?${qs.toString()}`;
}
