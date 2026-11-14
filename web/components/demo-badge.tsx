// #2043 — the "Demo" marker on a control or evidence record that the
// in-place demo seed (`atlas-cli demo seed` without --tenant-slug) added to
// a real organisation. The platform reads it from `demo_seed_rows` and sends
// `demo: true`; every surface an auditor reads shows this same badge, so demo
// evidence is never mistaken for real evidence.

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

export const DEMO_BADGE_TITLE =
  "Demo data: added by the demo seed, not part of your organisation's real records. `atlas-cli demo teardown` removes it.";

export function DemoBadge({
  className,
  testId = "demo-badge",
}: {
  className?: string;
  testId?: string;
}) {
  return (
    <Badge
      variant="warning"
      className={cn("ml-2", className)}
      title={DEMO_BADGE_TITLE}
      data-testid={testId}
    >
      Demo
    </Badge>
  );
}
