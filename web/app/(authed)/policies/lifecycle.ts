// #2116 — the policy workflow as the detail page shows it, and the
// create / edit form's rules. Pure, so the node vitest tier covers it.
//
// The platform enforces every rule here (draft-only edit and discard, the
// approval rule, the publish block on a policy with no linked control);
// this module only decides which action the header offers and what the
// form says before the operator submits.

import { ROLES } from "@/components/admin/roles";
import type {
  CreatePolicyInput,
  PendingAcknowledgment,
  Policy,
  PolicyAcknowledgment,
  PolicyDraftInput,
} from "@/lib/api/policies";

/** The header's primary lifecycle action for a policy's status. */
export type PolicyAction =
  | { kind: "submit"; label: "Submit for review" }
  | { kind: "approve"; label: "Approve" }
  | { kind: "publish"; label: "Publish"; href: string }
  | { kind: "new-version"; label: "Create new version"; href: string };

export function primaryAction(
  p: Pick<Policy, "id" | "status">,
): PolicyAction | null {
  const base = `/policies/${encodeURIComponent(p.id)}`;
  switch (p.status) {
    case "draft":
      return { kind: "submit", label: "Submit for review" };
    case "under_review":
      return { kind: "approve", label: "Approve" };
    case "approved":
      return { kind: "publish", label: "Publish", href: `${base}/publish` };
    case "published":
      return {
        kind: "new-version",
        label: "Create new version",
        href: `/policies/new?predecessor=${encodeURIComponent(p.id)}`,
      };
    default:
      // superseded: history, nothing to do.
      return null;
  }
}

/** Only a draft is edited in place or discarded; a published policy
 *  changes through a new version. */
export function isDraft(p: Pick<Policy, "status">): boolean {
  return p.status === "draft";
}

/** The publish block the platform enforces, said before the operator
 *  reaches it: on the form while no control is linked, and on a policy on
 *  its way to publishing. */
export const NO_CONTROLS_WARNING =
  "Publishing needs at least one linked control. You can save and submit this draft without one, but it cannot be published until a control is linked.";

export function needsControlsWarning(
  p: Pick<Policy, "status" | "linked_control_ids">,
): boolean {
  return (
    ["draft", "under_review", "approved"].includes(p.status) &&
    p.linked_control_ids.length === 0
  );
}

/** #2465 — where the signed-in person stands on this policy's
 *  acknowledgment. `pending` when the policy is published and is on the
 *  caller's pending list (their role is in its audience and they have not
 *  acknowledged this version within the year); `acknowledged` right after
 *  they do; otherwise null, and the page offers nothing. The platform
 *  decides who may acknowledge; this only mirrors its pending list. */
export type MyAckState =
  | { kind: "pending"; lastAcknowledgedAt: string | null }
  | { kind: "acknowledged"; acknowledgedAt: string };

export function myAckState(
  p: Pick<Policy, "id" | "status">,
  pending: readonly PendingAcknowledgment[] | undefined,
  justAcknowledged: PolicyAcknowledgment | undefined,
): MyAckState | null {
  if (justAcknowledged && justAcknowledged.policy_id === p.id) {
    return {
      kind: "acknowledged",
      acknowledgedAt: justAcknowledged.acknowledged_at,
    };
  }
  if (p.status !== "published" || !pending) return null;
  const mine = pending.find((a) => a.policy_id === p.id);
  return mine
    ? { kind: "pending", lastAcknowledgedAt: mine.last_acknowledged_at ?? null }
    : null;
}

/** The roles a policy can require to acknowledge it: the platform's RBAC
 *  roles, which acknowledgment matches against the user's own. */
export { ROLES as ACK_ROLES };

/** #2524 — the audience value that aims a policy at every member. */
export const AUDIENCE_EVERYONE = "everyone";

/** The audiences a policy is chosen from: the roles plus everyone. The
 *  platform refuses any other value on a new policy. */
export const AUDIENCE_OPTIONS: readonly string[] = [
  ...ROLES,
  AUDIENCE_EVERYONE,
];

/** The label the form and detail page show for an audience value. */
export function audienceLabel(value: string): string {
  return value === AUDIENCE_EVERYONE ? "Everyone (all members)" : value;
}

/** The values a policy carries that are not an audience: free text from
 *  before #2524 ("employee", "all_staff"). They still show, flagged, and
 *  no one holds them as a role. */
export function unrecognisedAudiences(values: readonly string[]): string[] {
  return values.filter((v) => !AUDIENCE_OPTIONS.includes(v));
}

export type PolicyFormValues = {
  title: string;
  version: string;
  owner_role: string;
  approver_role: string;
  linked_control_ids: string[];
  acknowledgment_required_roles: string[];
  body_md: string;
};

export type PolicyFormErrors = Partial<Record<keyof PolicyFormValues, string>>;

export const EMPTY_FORM: PolicyFormValues = {
  title: "",
  version: "1.0",
  owner_role: "",
  approver_role: "",
  linked_control_ids: [],
  acknowledgment_required_roles: [],
  body_md: "",
};

/** Required fields, as the platform requires them. `version` is checked
 *  only on create: an edit cannot change it. */
export function validatePolicyForm(
  v: PolicyFormValues,
  mode: "create" | "edit",
): PolicyFormErrors {
  const errs: PolicyFormErrors = {};
  if (!v.title.trim()) errs.title = "Title is required.";
  if (mode === "create" && !v.version.trim()) {
    errs.version = "Version is required.";
  }
  if (!v.owner_role.trim()) errs.owner_role = "Owner role is required.";
  if (!v.approver_role.trim()) {
    errs.approver_role = "Approver role is required.";
  }
  if (!v.body_md.trim()) errs.body_md = "Policy text is required.";
  // #2524: a new policy (or version) names roles or everyone. An edit may
  // keep a free-text value the draft already carries; the form never adds
  // one.
  const unknown = unrecognisedAudiences(v.acknowledgment_required_roles);
  if (mode === "create" && unknown.length > 0) {
    const names = unknown.join(", ");
    errs.acknowledgment_required_roles =
      "Replace the unrecognised audience " +
      names +
      " with a role or Everyone.";
  }
  return errs;
}

export function draftInput(v: PolicyFormValues): PolicyDraftInput {
  return {
    title: v.title.trim(),
    body_md: v.body_md,
    owner_role: v.owner_role.trim(),
    approver_role: v.approver_role.trim(),
    linked_control_ids: v.linked_control_ids,
    acknowledgment_required_roles: v.acknowledgment_required_roles,
  };
}

export function createInput(
  v: PolicyFormValues,
  predecessorId?: string,
): CreatePolicyInput {
  return {
    ...draftInput(v),
    version: v.version.trim(),
    ...(predecessorId ? { predecessor_id: predecessorId } : {}),
  };
}

/** The form a policy fills: an edit starts from the draft, and a new
 *  version from the published policy with its version bumped. */
export function formFromPolicy(
  p: Policy,
  as: "edit" | "new-version",
): PolicyFormValues {
  return {
    title: p.title,
    version: as === "edit" ? p.version : nextVersion(p.version),
    owner_role: p.owner_role,
    approver_role: p.approver_role,
    linked_control_ids: [...p.linked_control_ids],
    acknowledgment_required_roles: [...p.acknowledgment_required_roles],
    body_md: p.body_md,
  };
}

/** The suggested next version: the last number bumped ("1.0" -> "1.1",
 *  "v2" -> "v3"); a version with no number gets ".1" appended. The
 *  operator can change it; the platform only requires that it differ. */
export function nextVersion(v: string): string {
  const m = /^(.*?)(\d+)(\D*)$/.exec(v);
  if (!m) return v ? `${v}.1` : "1.0";
  return `${m[1]}${Number(m[2]) + 1}${m[3]}`;
}
