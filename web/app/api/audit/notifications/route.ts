// Slice 042 — audit workspace BFF: GET /v1/me/notifications proxy (slice 029).
//
// Returns the caller's notifications + unread_count. The platform scopes
// to caller.UserID — the auditor only ever sees their own notifications.

import { forwardJSON } from "@/lib/api/bff";

export async function GET() {
  return forwardJSON("/v1/me/notifications");
}
