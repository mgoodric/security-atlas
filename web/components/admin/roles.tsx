// Slice 060 — shared role-permission matrix.
//
// This module is the canonical source for the five RBAC roles defined in
// Plans/canvas/09-tech-stack.md §9.5. It exists in the frontend (not just
// the backend) because the admin Users page renders the matrix and the
// HITL reviewer signs off on it on the slice 060 PR. The wording here is
// the wording that hits Matt's eyes during review.
//
// If the matrix drifts from the backend Rego policies (slice 035), this
// is the bug — update both in the same PR.

import { Badge } from "@/components/ui/badge";

export const ROLES = [
  "admin",
  "grc_engineer",
  "control_owner",
  "auditor",
  "viewer",
] as const;

export type Role = (typeof ROLES)[number];

export const ROLE_DESCRIPTIONS: Record<
  Role,
  { oneLine: string; permissions: string[] }
> = {
  admin: {
    oneLine: "Full platform configuration + role assignment.",
    permissions: [
      "Configure SSO, issue/rotate/revoke API keys, toggle feature flags",
      "Assign and revoke roles on other users",
      "Read every audit log surface across the union",
      "Override any control owner / approver workflow",
    ],
  },
  grc_engineer: {
    oneLine: "Authors controls, mappings, and policies.",
    permissions: [
      "Create + edit controls, control bundles, framework mappings",
      "Author and submit policies for approval",
      "Configure connectors (push credentials carry connector scope)",
      "Read all evidence; cannot change role assignments",
    ],
  },
  control_owner: {
    oneLine: "Operates the controls they own.",
    permissions: [
      "Attest manual controls assigned to their owner_roles",
      "Read evidence + control state for owned controls",
      "Acknowledge policies",
      "Cannot change controls, mappings, or other users' roles",
    ],
  },
  auditor: {
    oneLine: "External read-only access for an audit window.",
    permissions: [
      "Read controls, evidence, samples, and the audit-period control state",
      "Annotate samples (passed / failed / not-applicable)",
      "Cannot change controls, evidence, scopes, or roles",
      "Access is ABAC-narrowed to a specific audit_period scope",
    ],
  },
  viewer: {
    oneLine: "Read-only access for stakeholders.",
    permissions: [
      "Read dashboards, controls, evidence summaries",
      "Cannot read raw audit log, API keys, or SSO config",
      "Cannot change anything",
    ],
  },
};

export function RoleBadge({ role }: { role: Role }) {
  const variant: "default" | "secondary" | "outline" | "destructive" =
    role === "admin"
      ? "destructive"
      : role === "viewer"
        ? "outline"
        : "secondary";
  return (
    <Badge variant={variant} className="font-mono text-[10px] uppercase">
      {role}
    </Badge>
  );
}
