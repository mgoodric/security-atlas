// /admin/audit — target of the admin overview's "Audit log" tile.
//
// Activity is the one audit-log page (#2093); this route forwards to it.
// /activity reads through `/v1/activity/unified`, which every tenant
// member already reaches, so the redirect grants nothing new. Callers
// the export endpoint admits see the export bar there.

import { redirect } from "next/navigation";

export default function AdminAuditRedirect() {
  redirect("/activity");
}
