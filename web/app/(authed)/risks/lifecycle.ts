// #2105 — a risk's treatment and acceptance transitions, as pure
// functions so the node vitest tier covers them (slice 069 P0-A3). The
// risk page and its /treatment and /acceptance/[action] form routes
// render them.
//
// The state machine itself is the platform's (internal/risk/transitions.go):
// these functions choose which action the page offers and build each
// form's body. A risk with an acceptance in flight changes treatment only
// by rejecting or revoking it first, so the page offers no treatment
// change then.

import type {
  Risk,
  RiskClosureReason,
  RiskTransitionBody,
} from "@/lib/api/risks";
import { enumLabel } from "@/lib/labels/enum-labels";

/** Where a risk stands in the acceptance lifecycle. */
export type AcceptanceState = "none" | "proposed" | "approved";

export function acceptanceState(
  r: Pick<Risk, "treatment" | "acceptance_status">,
): AcceptanceState {
  if (r.treatment !== "accept") return "none";
  return r.acceptance_status === "approved" ? "approved" : "proposed";
}

/** The treatments "Change treatment" offers; accept is proposed instead. */
export const CHANGE_TREATMENTS = [
  "untreated",
  "mitigate",
  "transfer",
  "avoid",
] as const;

export type ChangeTreatment = (typeof CHANGE_TREATMENTS)[number];

export function isChangeTreatment(s: string): s is ChangeTreatment {
  return (CHANGE_TREATMENTS as readonly string[]).includes(s);
}

/** The acceptance actions, each on its own form route. */
export type AcceptanceAction = "propose" | "approve" | "reject" | "revoke";

export const ACCEPTANCE_ACTIONS: readonly AcceptanceAction[] = [
  "propose",
  "approve",
  "reject",
  "revoke",
];

export function isAcceptanceAction(s: string): s is AcceptanceAction {
  return (ACCEPTANCE_ACTIONS as readonly string[]).includes(s);
}

/** The state each action starts from; the platform 409s any other. */
export const ACTION_FROM: Record<AcceptanceAction, AcceptanceState> = {
  propose: "none",
  approve: "proposed",
  reject: "proposed",
  revoke: "approved",
};

export const ACTION_COPY: Record<
  AcceptanceAction,
  {
    title: string;
    submit: string;
    busy: string;
    done: string;
    help: string;
    onlyFrom: string;
  }
> = {
  propose: {
    title: "Propose acceptance",
    submit: "Propose acceptance",
    busy: "Proposing…",
    done: "Acceptance proposed",
    help: "Accepting a risk needs a second person's approval. Say why it should be accepted and until when.",
    onlyFrom:
      "A risk with an acceptance already proposed or approved cannot be proposed again.",
  },
  approve: {
    title: "Approve acceptance",
    submit: "Approve acceptance",
    busy: "Approving…",
    done: "Acceptance approved",
    help: "You become the accepter. The person who proposed the acceptance cannot approve it unless your organisation allows self-approval.",
    onlyFrom: "Only a proposed acceptance can be approved.",
  },
  reject: {
    title: "Reject acceptance",
    submit: "Reject acceptance",
    busy: "Rejecting…",
    done: "Acceptance rejected",
    help: "The risk returns to untreated. Its owner chooses a treatment or proposes again.",
    onlyFrom: "Only a proposed acceptance can be rejected.",
  },
  revoke: {
    title: "Revoke acceptance",
    submit: "Revoke acceptance",
    busy: "Revoking…",
    done: "Acceptance revoked",
    help: "The risk returns to untreated and counts as open exposure again.",
    onlyFrom: "Only an approved acceptance can be revoked.",
  },
};

/** The one lifecycle action the risk page leads with for a state. */
export function primaryAcceptanceAction(
  state: AcceptanceState,
): AcceptanceAction {
  switch (state) {
    case "none":
      return "propose";
    case "proposed":
      return "approve";
    case "approved":
      return "revoke";
  }
}

const DAY_MS = 24 * 60 * 60 * 1000;

/** YYYY-MM-DD for the UTC day `days` after `now`. */
export function dayAfter(now: Date, days: number): string {
  return new Date(now.getTime() + days * DAY_MS).toISOString().slice(0, 10);
}

function isDay(s: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return false;
  return !Number.isNaN(new Date(`${s}T00:00:00Z`).getTime());
}

/** True when day (YYYY-MM-DD) is after now's UTC day. */
function afterToday(day: string, now: Date): boolean {
  return day > now.toISOString().slice(0, 10);
}

export type AcceptanceDraft = {
  justification: string;
  /** YYYY-MM-DD: the proposed expiry (propose) or accepted_until (approve). */
  until: string;
  reason: string;
};

export type ActionResult =
  | { ok: true; body: RiskTransitionBody }
  | { ok: false; error: string };

/** Checks an acceptance form and builds its request body. */
export function validateAcceptance(
  action: AcceptanceAction,
  draft: AcceptanceDraft,
  now: Date,
): ActionResult {
  const reason = draft.reason.trim();
  switch (action) {
    case "propose": {
      const justification = draft.justification.trim();
      if (!justification)
        return { ok: false, error: "Say why the risk should be accepted." };
      if (!isDay(draft.until))
        return { ok: false, error: "Choose the proposed expiry." };
      if (!afterToday(draft.until, now))
        return { ok: false, error: "The expiry must be after today." };
      return { ok: true, body: { justification, proposed_until: draft.until } };
    }
    case "approve":
      if (!isDay(draft.until))
        return { ok: false, error: "Choose the date the acceptance lapses." };
      if (!afterToday(draft.until, now))
        return { ok: false, error: "The expiry must be after today." };
      return { ok: true, body: { accepted_until: draft.until } };
    case "reject":
      return { ok: true, body: reason ? { reason } : {} };
    case "revoke":
      if (!reason) return { ok: false, error: "Say why it is revoked." };
      return { ok: true, body: { reason } };
  }
}

export type TreatmentDraft = { treatment: string; instrumentReference: string };

/** Checks the change-treatment form and builds its request body. */
export function validateTreatmentChange(draft: TreatmentDraft): ActionResult {
  if (!isChangeTreatment(draft.treatment))
    return { ok: false, error: "Choose a treatment." };
  if (draft.treatment === "transfer") {
    const ref = draft.instrumentReference.trim();
    if (!ref)
      return {
        ok: false,
        error: "A transfer needs the policy, contract or SOW it relies on.",
      };
    return {
      ok: true,
      body: { treatment: draft.treatment, instrument_reference: ref },
    };
  }
  return { ok: true, body: { treatment: draft.treatment } };
}

// ----- #2107: the open/closed lifecycle -----

/** The closure reasons, in the order the close form lists them. */
export const CLOSURE_REASONS: readonly RiskClosureReason[] = [
  "not_applicable",
  "merged",
  "realised_and_handled",
];

/** A closure reason's label, from the shared enum labels. */
export function closureReasonLabel(reason: RiskClosureReason): string {
  return enumLabel("RiskClosureReason", reason);
}

export function isClosureReason(s: string): s is RiskClosureReason {
  return (CLOSURE_REASONS as readonly string[]).includes(s);
}

export type CloseDraft = { reason: string; note: string };

/** Checks the close form and builds its request body. */
export function validateClose(draft: CloseDraft): ActionResult {
  if (!isClosureReason(draft.reason))
    return { ok: false, error: "Choose why the risk is closed." };
  const note = draft.note.trim();
  return {
    ok: true,
    body: note ? { reason: draft.reason, note } : { reason: draft.reason },
  };
}
