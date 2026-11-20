// #2113 — the exception request form and lifecycle actions, as pure
// functions so the node vitest tier covers them (slice 069 P0-A3). The
// pages under /exceptions render them.
//
// The one security-relevant rule: the scope picker emits null while
// "choose by dimension" has no value picked, and the platform reads a
// stored null predicate as every cell. A null predicate is therefore an
// incomplete form, and `validateRequest` returns no body for it, so the
// request cannot be submitted. The whole control is the explicit
// always-true predicate `{"op":"true"}`, the picker's Everything.

import type {
  CreateExceptionInput,
  ExceptionStatus,
  ExceptionTransition,
} from "@/lib/api/exceptions";

/** The platform's cap on an exception's lifetime (internal/exception MaxLifetime). */
export const MAX_DAYS = 365;

/** The expiry the form starts with. */
export const DEFAULT_DAYS = 90;

const DAY_MS = 24 * 60 * 60 * 1000;

/** YYYY-MM-DD for the UTC day `days` after `now`. */
export function dayAfter(now: Date, days: number): string {
  return new Date(now.getTime() + days * DAY_MS).toISOString().slice(0, 10);
}

/** RFC 3339 for midnight UTC on a YYYY-MM-DD day, or null if malformed. */
export function dayStart(day: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return null;
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return null;
  return d.toISOString();
}

export type RequestDraft = {
  controlId: string;
  /** The picker's emission: a predicate, or null while incomplete. */
  predicate: object | null;
  justification: string;
  /** One compensating control per line. */
  compensating: string;
  /** YYYY-MM-DD. */
  expiresOn: string;
};

export type RequestErrors = Partial<
  Record<"control" | "scope" | "justification" | "expires", string>
>;

export type RequestResult =
  | { ok: true; body: CreateExceptionInput }
  | { ok: false; errors: RequestErrors };

/**
 * Checks a draft and builds the request body. No body is built while any
 * field is wrong, and never for a null predicate.
 */
export function validateRequest(draft: RequestDraft, now: Date): RequestResult {
  const errors: RequestErrors = {};
  if (!draft.controlId) errors.control = "Choose the control to except.";

  const p: unknown = draft.predicate;
  if (p === null || typeof p !== "object" || Array.isArray(p)) {
    errors.scope = "Pick at least one value, or choose Everything.";
  }

  const justification = draft.justification.trim();
  if (!justification) {
    errors.justification = "Say why the control cannot be met.";
  }

  const expiresAt = dayStart(draft.expiresOn);
  if (!expiresAt) {
    errors.expires = "Choose an expiry date.";
  } else {
    const t = new Date(expiresAt).getTime();
    if (t <= now.getTime()) {
      errors.expires = "The expiry must be after today.";
    } else if (t > now.getTime() + MAX_DAYS * DAY_MS) {
      errors.expires = `An exception lasts at most ${MAX_DAYS} days. Choose ${dayAfter(
        now,
        MAX_DAYS,
      )} or earlier.`;
    }
  }

  if (Object.keys(errors).length > 0 || !expiresAt) {
    return { ok: false, errors };
  }
  return {
    ok: true,
    body: {
      control_id: draft.controlId,
      scope_cell_predicate: draft.predicate as object,
      justification,
      compensating_controls: draft.compensating
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean),
      expires_at: expiresAt,
    },
  };
}

/** The actions that open their own form route (they take input). */
export type FormAction = Exclude<ExceptionTransition, "approve">;

export const FORM_ACTIONS: readonly FormAction[] = [
  "deny",
  "activate",
  "revoke",
];

export function isFormAction(s: string): s is FormAction {
  return (FORM_ACTIONS as readonly string[]).includes(s);
}

/** The state each action applies to; the platform 409s any other. */
export const ACTION_FROM: Record<ExceptionTransition, ExceptionStatus> = {
  approve: "requested",
  deny: "requested",
  activate: "approved",
  revoke: "active",
};

export const ACTION_COPY: Record<
  FormAction,
  {
    title: string;
    submit: string;
    busy: string;
    done: string;
    help: string;
    /** Shown when the exception is not in ACTION_FROM's state. */
    onlyFrom: string;
  }
> = {
  deny: {
    title: "Deny exception",
    submit: "Deny",
    busy: "Denying…",
    done: "Exception denied",
    help: "Denied is final. The requester files a new exception to try again.",
    onlyFrom: "Only a requested exception can be denied.",
  },
  activate: {
    title: "Activate exception",
    submit: "Activate",
    busy: "Activating…",
    done: "Exception activated",
    help: "From the effective date the control evaluates as excepted in the covered scope, until the exception expires or is revoked.",
    onlyFrom: "Only an approved exception can be activated.",
  },
  revoke: {
    title: "Revoke exception",
    submit: "Revoke",
    busy: "Revoking…",
    done: "Exception revoked",
    help: "Revoking ends the exception now. The control is re-evaluated straight away and shows its own result again. Revoked is final.",
    onlyFrom: "Only an active exception can be revoked.",
  },
};

export type ActionResult =
  | { ok: true; body: { reason?: string; effective_from?: string } }
  | { ok: false; error: string };

/** Checks an action form: deny and revoke need a reason; activate takes an
 *  optional effective-from day (blank means now). */
export function validateAction(
  action: FormAction,
  input: { reason: string; effectiveOn: string },
): ActionResult {
  if (action === "activate") {
    if (!input.effectiveOn) return { ok: true, body: {} };
    const from = dayStart(input.effectiveOn);
    if (!from) return { ok: false, error: "Choose a valid effective date." };
    return { ok: true, body: { effective_from: from } };
  }
  const reason = input.reason.trim();
  if (!reason) return { ok: false, error: "Give a reason." };
  return { ok: true, body: { reason } };
}

export type NextAction =
  | { kind: "approve" }
  | { kind: "link"; action: FormAction; label: string }
  | null;

/** The one primary lifecycle action for a state, or null when final. */
export function nextAction(status: ExceptionStatus): NextAction {
  switch (status) {
    case "requested":
      return { kind: "approve" };
    case "approved":
      return { kind: "link", action: "activate", label: "Activate" };
    case "active":
      return { kind: "link", action: "revoke", label: "Revoke" };
    default:
      return null;
  }
}

/** Status pill colours, shared by the list and the detail page. */
export function statusPillClass(status: string): string {
  switch (status) {
    case "active":
      return "bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    case "requested":
    case "approved":
      return "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "denied":
      return "bg-rose-50 text-rose-700 dark:bg-rose-950 dark:text-rose-300";
    case "expired":
    case "revoked":
      return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
    default:
      return "bg-muted text-muted-foreground";
  }
}

/** The Idempotency-Key a request form last sent, and for which body. */
export type SentKey = { body: string; key: string };

/**
 * #2618 — the Idempotency-Key for submitting `body`. Submitting the same
 * body again (a retry after a timeout or an error) reuses the last key,
 * so the platform replays what the first send filed instead of filing a
 * duplicate. A changed body is a new request and gets a fresh key.
 */
export function idempotencyKeyFor(
  last: SentKey | null,
  body: string,
  freshKey: () => string,
): SentKey {
  return last && last.body === body ? last : { body, key: freshKey() };
}

/** The per-control key in a bulk request: one exception per control. */
export function bulkIdempotencyKey(key: string, controlId: string): string {
  return `${key}:${controlId}`;
}
