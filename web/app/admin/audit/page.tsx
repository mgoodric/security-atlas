// /admin/audit — target of the admin overview's "Audit log" tile.
//
// The unified audit log ships at /audit-log, whose own server-side layout
// guard decides access (it admits auditor and grc_engineer as well as
// admin, so the redirect grants nothing new). This route
// used to hold a pre-backend scaffold that described the missing endpoint
// to the user; it now forwards to the real page (#2082).

import { redirect } from "next/navigation";

export default function AdminAuditRedirect() {
  redirect("/audit-log");
}
