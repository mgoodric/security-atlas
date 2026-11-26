// #2174 — the "owner can't access this module" flag beside a record's
// owner. The platform computes `owner_lacks_access` at read time from the
// owner's live roles and module access; the owner stays assigned, and the
// flag tells whoever can see the record that it needs reassigning.

import { Badge } from "@/components/ui/badge";

export function OwnerAccessFlag({
  lacksAccess,
  testId,
}: {
  lacksAccess: boolean | undefined;
  testId: string;
}) {
  if (!lacksAccess) return null;
  return (
    <Badge
      variant="warning"
      data-testid={testId}
      title="Their module access no longer includes this module. Reassign the record."
    >
      Owner can&apos;t access this module
    </Badge>
  );
}
