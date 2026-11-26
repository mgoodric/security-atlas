// #2101 — the "Self-approved" marker. A record whose requester approved it
// under the organisation's Allow self-approval setting carries the
// `self_approved` flag; every surface that shows such a record shows this
// marker, so a self-approval is never mistaken for a second person's.

import { Badge } from "@/components/ui/badge";

export function SelfApprovedBadge({
  selfApproved,
  testId,
}: {
  selfApproved?: boolean | null;
  testId: string;
}) {
  if (!selfApproved) return null;
  return (
    <Badge
      variant="outline"
      data-testid={testId}
      title="Approved by the person who requested it, under the organisation's Allow self-approval setting"
    >
      Self-approved
    </Badge>
  );
}
