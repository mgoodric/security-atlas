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

/**
 * One refused item: the control and the platform's reason. `kind` marks
 * the refusals the summary collapses into one line (#2618): rate-limited
 * after the retries ran out, or cancelled before the item ran.
 */
export type Refusal = {
  id: string;
  reason: string;
  kind?: "rate_limited" | "cancelled";
  /** The last Retry-After seen for a rate-limited item, in seconds. */
  retryAfterSeconds?: number;
};

/** The outcome of a per-control bulk write. */
export type PerItemResult = {
  succeeded: string[];
  refused: Refusal[];
};

// How many per-control writes run at once. Small: the platform rate-limits
// evaluation per principal, and a bounded fan-out keeps a 200-control
// batch from opening 200 sockets.
export const BULK_CONCURRENCY = 4;

// #2618 — how a rate-limited write is retried. A 429 that names a
// Retry-After pauses the whole batch for that long, then the item runs
// again. After MAX_RATE_LIMIT_ATTEMPTS tries, or for a wait longer than
// MAX_RETRY_AFTER_SECONDS, the item is refused as rate-limited.
export const MAX_RATE_LIMIT_ATTEMPTS = 10;
export const MAX_RETRY_AFTER_SECONDS = 120;

/** Progress through a batch: how many succeeded and settled, of total. */
export type Progress = { succeeded: number; settled: number; total: number };

export type RunOptions = {
  concurrency?: number;
  /** Aborting stops the batch: items not yet run are refused as cancelled. */
  signal?: AbortSignal;
  /** Called once per settled item. */
  onProgress?: (p: Progress) => void;
  /** Waits `ms`, rejecting when `signal` aborts. Injected by tests. */
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
};

class Cancelled extends Error {}

/** The default sleep: a timer that rejects with Cancelled on abort. */
export function abortableSleep(
  ms: number,
  signal?: AbortSignal,
): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new Cancelled());
    const onAbort = () => {
      clearTimeout(t);
      reject(new Cancelled());
    };
    const t = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/** The HTTP status and Retry-After an error carries, if any. */
function statusOf(err: unknown): { status?: number; wait?: number } {
  if (typeof err !== "object" || err === null) return {};
  const e = err as { status?: unknown; retryAfterSeconds?: unknown };
  return {
    status: typeof e.status === "number" ? e.status : undefined,
    wait:
      typeof e.retryAfterSeconds === "number" ? e.retryAfterSeconds : undefined,
  };
}

/**
 * Runs `write` once per id, at most `concurrency` at a time, and sorts
 * each id into succeeded or refused. A thrown error is a refusal carrying
 * its message; nothing short-circuits the rest of the batch. The result
 * keeps the input order.
 *
 * #2618: a 429 with a Retry-After pauses every worker for that long and
 * retries the item. Aborting `signal` refuses as cancelled every item not
 * yet started and any item waiting out a Retry-After; a write already in
 * flight finishes.
 */
export async function runPerItem(
  ids: readonly string[],
  write: (id: string) => Promise<unknown>,
  opts: RunOptions = {},
): Promise<PerItemResult> {
  const concurrency = opts.concurrency ?? BULK_CONCURRENCY;
  const sleep = opts.sleep ?? abortableSleep;
  const { signal } = opts;
  const outcome: (Refusal | null)[] = new Array(ids.length);
  let next = 0;
  let succeeded = 0;
  let settled = 0;
  // The earliest time any worker may start a write, after a Retry-After.
  let pausedUntil = 0;

  async function runOne(id: string): Promise<Refusal | null> {
    for (let attempt = 1; ; attempt++) {
      const pause = pausedUntil - Date.now();
      if (pause > 0) await sleep(pause, signal);
      if (signal?.aborted) throw new Cancelled();
      try {
        await write(id);
        return null;
      } catch (err) {
        const { status, wait } = statusOf(err);
        if (
          status === 429 &&
          wait !== undefined &&
          wait <= MAX_RETRY_AFTER_SECONDS &&
          attempt < MAX_RATE_LIMIT_ATTEMPTS
        ) {
          pausedUntil = Math.max(pausedUntil, Date.now() + wait * 1000);
          continue;
        }
        const reason =
          err instanceof Error && err.message ? err.message : "Refused";
        return status === 429
          ? { id, reason, kind: "rate_limited", retryAfterSeconds: wait }
          : { id, reason };
      }
    }
  }

  async function worker() {
    while (next < ids.length) {
      const i = next++;
      let o: Refusal | null;
      try {
        if (signal?.aborted) throw new Cancelled();
        o = await runOne(ids[i]);
      } catch (err) {
        if (!(err instanceof Cancelled)) throw err;
        o = { id: ids[i], reason: "Cancelled", kind: "cancelled" };
      }
      outcome[i] = o;
      settled += 1;
      if (o === null) succeeded += 1;
      opts.onProgress?.({ succeeded, settled, total: ids.length });
    }
  }
  const n = Math.max(1, Math.min(concurrency, ids.length));
  await Promise.all(Array.from({ length: n }, worker));
  const ok: string[] = [];
  const refused: Refusal[] = [];
  outcome.forEach((o, i) => {
    if (o) refused.push(o);
    else ok.push(ids[i]);
  });
  return { succeeded: ok, refused };
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

/**
 * Builds the region's message from a per-item result. Rate-limited and
 * cancelled refusals each collapse into one line ("14 controls not
 * evaluated: rate limit reached, retry in ~60s") rather than repeating
 * one reason per control (#2618).
 */
export function perItemMessage(
  result: PerItemResult,
  done: string,
  labelOf: (id: string) => string,
): BulkMessage {
  const limited = result.refused.filter((r) => r.kind === "rate_limited");
  const cancelled = result.refused.filter((r) => r.kind === "cancelled");
  const refusals = result.refused
    .filter((r) => r.kind === undefined)
    .map((r) => ({ label: labelOf(r.id), reason: r.reason }));
  const notDone = (k: number) =>
    `${k} control${k === 1 ? "" : "s"} not ${done}`;
  if (limited.length > 0) {
    const waits = limited
      .map((r) => r.retryAfterSeconds)
      .filter((w): w is number => w !== undefined);
    const hint =
      waits.length > 0 ? `retry in ~${Math.max(...waits)}s` : "retry later";
    refusals.push({
      label: notDone(limited.length),
      reason: `rate limit reached, ${hint}`,
    });
  }
  if (cancelled.length > 0) {
    refusals.push({ label: notDone(cancelled.length), reason: "cancelled" });
  }
  return {
    kind: result.refused.length === 0 ? "ok" : "error",
    text: summarize(result, done),
    refusals,
  };
}

/** The request-exception form, opened for the selected controls. */
export function exceptionRequestHref(ids: readonly string[]): string {
  const qs = new URLSearchParams();
  for (const id of ids) qs.append("control_id", id);
  return `/exceptions/new?${qs.toString()}`;
}
