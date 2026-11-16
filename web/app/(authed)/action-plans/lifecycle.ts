// #2108 — the action-plan stepper's transitions, as pure functions so the
// rules are covered in the node vitest tier.
//
// The buttons are UI only: the platform's state machine
// (internal/actionplan/statemachine.go AllowedTransition) and the DB
// transition trigger are the authority. These tables mirror them so the
// page offers only moves the platform accepts.

import type { ActionPlanStatus } from "@/lib/api/action-plans";

export type PlanTransitionId =
  | "start"
  | "block"
  | "unblock"
  | "complete"
  | "reopen"
  | "verify";

export type PlanTransition = {
  id: PlanTransitionId;
  label: string;
  to: ActionPlanStatus;
  /** The header's primary action; the rest go in the overflow menu. */
  primary: boolean;
  /** The inline confirm's question. */
  prompt: string;
  /** The toast on success. */
  toast: string;
};

const T: Record<PlanTransitionId, Omit<PlanTransition, "primary">> = {
  start: {
    id: "start",
    label: "Start",
    to: "in_progress",
    prompt: "Start this plan?",
    toast: "Plan started",
  },
  block: {
    id: "block",
    label: "Block",
    to: "blocked",
    prompt: "Mark this plan blocked?",
    toast: "Plan blocked",
  },
  unblock: {
    id: "unblock",
    label: "Unblock",
    to: "in_progress",
    prompt: "Unblock this plan? It goes back to In progress.",
    toast: "Plan unblocked",
  },
  complete: {
    id: "complete",
    label: "Mark complete",
    to: "completed",
    prompt: "Mark this plan complete?",
    toast: "Plan marked complete",
  },
  reopen: {
    id: "reopen",
    label: "Reopen",
    to: "in_progress",
    prompt: "Reopen this plan? It goes back to In progress.",
    toast: "Plan reopened",
  },
  verify: {
    id: "verify",
    label: "Verify",
    to: "verified",
    prompt: "Verify this plan? Verified is final.",
    toast: "Plan verified",
  },
};

export type VerifyContext = {
  /** The plan has at least one linked control. Its controls verify it. */
  hasLinkedControls: boolean;
  /** The viewer holds the approver role and is not the plan's owner. */
  canVerify: boolean;
};

/**
 * The transitions to offer from `status`, primary first. Verify is offered
 * only on a completed plan with no linked controls, and only to an
 * approver who is not the plan's owner.
 */
export function planTransitions(
  status: ActionPlanStatus,
  ctx: VerifyContext,
): PlanTransition[] {
  const pick = (primary: PlanTransitionId, ...rest: PlanTransitionId[]) => [
    { ...T[primary], primary: true },
    ...rest.map((id) => ({ ...T[id], primary: false })),
  ];
  switch (status) {
    case "draft":
      return pick("start");
    case "in_progress":
      return pick("complete", "block");
    case "blocked":
      return pick("unblock", "complete");
    case "completed":
      if (!ctx.hasLinkedControls && ctx.canVerify) {
        return pick("verify", "reopen");
      }
      return [{ ...T.reopen, primary: false }];
    case "verified":
      return [];
  }
}

/** A users(id) as a bare uuid: a JWT subject carries a "user:" prefix. */
export function bareUserId(id: string | null | undefined): string {
  return (id ?? "").replace(/^user:/, "").toLowerCase();
}

/**
 * Whether the viewer may verify a plan: the approver role (admin implies
 * it for a signed-in person) and not the plan's owner. The platform makes
 * the same check; this only decides whether to show the button.
 */
export function viewerCanVerify(
  me: { user_id: string; is_admin: boolean } | undefined,
  ownerId: string,
): boolean {
  if (!me || !me.is_admin) return false;
  const viewer = bareUserId(me.user_id);
  return viewer !== "" && viewer !== bareUserId(ownerId);
}
