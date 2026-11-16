// #1778 — bulk-approve of unchanged remap proposals. One call to the
// platform's bulk endpoint (never a client-side loop of single approves);
// the platform checks the role and that the caller is a person.

import { forwardJSON } from "@/lib/api/bff";

export function POST() {
  return forwardJSON(`/v1/controls/remap-proposals/approve-unchanged`, {
    method: "POST",
  });
}
