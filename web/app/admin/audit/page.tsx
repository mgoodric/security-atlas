// /admin/audit — target of the admin overview's "Audit log" tile.
//
// The unified audit log ships at /audit-log (same admin guard). This route
// used to hold a pre-backend scaffold that described the missing endpoint
// to the user; it now forwards to the real page (#2082).

import { redirect } from "next/navigation";

export default function AdminAuditRedirect() {
  redirect("/audit-log");
}
